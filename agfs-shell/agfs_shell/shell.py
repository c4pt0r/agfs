"""Shell implementation with REPL and command execution"""

import sys
import os
from typing import Optional, List
from rich.console import Console
from .parser import CommandParser
from .pipeline import Pipeline
from .process import Process
from .streams import InputStream, OutputStream, ErrorStream
from .builtins import get_builtin
from .filesystem import AGFSFileSystem
from .command_decorators import CommandMetadata
from pyagfs import AGFSClientError
from . import __version__
from .exit_codes import (
    EXIT_CODE_CONTINUE,
    EXIT_CODE_BREAK,
    EXIT_CODE_FOR_LOOP_NEEDED,
    EXIT_CODE_WHILE_LOOP_NEEDED,
    EXIT_CODE_IF_STATEMENT_NEEDED,
    EXIT_CODE_HEREDOC_NEEDED,
    EXIT_CODE_FUNCTION_DEF_NEEDED,
    EXIT_CODE_RETURN
)
from .control_flow import BreakException, ContinueException, ReturnException
from .control_parser import ControlParser
from .executor import ShellExecutor
from .expression import ExpressionExpander


class Shell:
    """Simple shell with pipeline support"""

    def __init__(self, server_url: str = "http://localhost:8080", timeout: int = 30):
        self.parser = CommandParser()
        self.running = True
        self.filesystem = AGFSFileSystem(server_url, timeout=timeout)
        self.server_url = server_url
        self.cwd = '/'  # Current working directory
        self.console = Console(highlight=False)  # Rich console for output
        self.multiline_buffer = []  # Buffer for multiline input
        self.env = {}  # Environment variables
        self.env['?'] = '0'  # Last command exit code

        # Set default history file location
        import os
        home = os.path.expanduser("~")
        self.env['HISTFILE'] = os.path.join(home, ".agfs_shell_history")

        self.interactive = False  # Flag to indicate if running in interactive REPL mode

        # Function definitions: {name: {'params': [...], 'body': [...]}}
        self.functions = {}

        # Variable scope stack for local variables
        # Each entry is a dict of local variables for that scope
        self.local_scopes = []

        # Control flow components
        self.control_parser = ControlParser(self)
        self.executor = ShellExecutor(self)

        # Expression expander (unified variable/arithmetic/command substitution)
        self.expression_expander = ExpressionExpander(self)

    def _execute_command_substitution(self, command: str) -> str:
        """
        Execute a command and return its output as a string
        Used for command substitution: $(command) or `command`
        """
        from .streams import OutputStream, InputStream, ErrorStream
        from .builtins import get_builtin

        # Parse and execute the command, capturing stdout
        try:
            # Expand variables AND arithmetic, but handle command substitution carefully
            # We need full expansion for the command
            command = self._expand_variables(command)

            # Parse the command
            commands, redirections = self.parser.parse_command_line(command)
            if not commands:
                return ''

            # Check if this is a user-defined function call (single command only)
            if len(commands) == 1:
                cmd, args = commands[0]
                if cmd in self.functions:
                    # Execute the function and capture all its output
                    # We need to capture at the stream level, not sys.stdout
                    import io

                    # Create a buffer to capture output
                    output_buffer = io.BytesIO()

                    # Save real stdout buffer
                    import sys
                    old_stdout_buffer = sys.stdout.buffer if hasattr(sys.stdout, 'buffer') else None

                    # Create a wrapper that has .buffer attribute
                    class StdoutWrapper:
                        def __init__(self, buffer):
                            self._buffer = buffer
                        @property
                        def buffer(self):
                            return self._buffer
                        def write(self, s):
                            if isinstance(s, str):
                                self._buffer.write(s.encode('utf-8'))
                            else:
                                self._buffer.write(s)
                        def flush(self):
                            pass

                    # Temporarily replace sys.stdout
                    old_stdout = sys.stdout
                    sys.stdout = StdoutWrapper(output_buffer)

                    try:
                        # Execute the function
                        exit_code = self.execute_function(cmd, args)

                        # Get all captured output
                        output = output_buffer.getvalue().decode('utf-8')
                        # Remove trailing newline if present
                        if output.endswith('\n'):
                            output = output[:-1]
                        return output

                    finally:
                        # Restore stdout
                        sys.stdout = old_stdout

            # Build processes for each command (simplified, no redirections)
            processes = []
            for i, (cmd, args) in enumerate(commands):
                executor = get_builtin(cmd)

                # Resolve paths for file commands (using metadata instead of hardcoded list)
                if CommandMetadata.needs_path_resolution(cmd):
                    resolved_args = []
                    skip_next = False
                    for j, arg in enumerate(args):
                        # Skip if this is a flag value (e.g., the "2" in "-n 2")
                        if skip_next:
                            resolved_args.append(arg)
                            skip_next = False
                            continue

                        # Skip flags (starting with -)
                        if arg.startswith('-'):
                            resolved_args.append(arg)
                            # Check if this flag takes a value (e.g., -n, -L, -d, -f)
                            if arg in ['-n', '-L', '-d', '-f', '-t', '-c'] and j + 1 < len(args):
                                skip_next = True
                            continue

                        # Skip pure numbers (they're likely option values, not paths)
                        try:
                            float(arg)
                            resolved_args.append(arg)
                            continue
                        except ValueError:
                            pass

                        # Resolve path
                        resolved_args.append(self.resolve_path(arg))
                    args = resolved_args

                # Create streams - always capture to buffer
                stdin = InputStream.from_bytes(b'')
                stdout = OutputStream.to_buffer()
                stderr = ErrorStream.to_buffer()

                # Create process
                process = Process(
                    command=cmd,
                    args=args,
                    stdin=stdin,
                    stdout=stdout,
                    stderr=stderr,
                    executor=executor,
                    filesystem=self.filesystem,
                    env=self.env,
                    shell=self
                )
                process.cwd = self.cwd
                processes.append(process)

            # Execute pipeline sequentially, like Pipeline class
            for i, process in enumerate(processes):
                # If this is not the first process, connect previous stdout to this stdin
                if i > 0:
                    prev_process = processes[i - 1]
                    prev_output = prev_process.get_stdout()
                    process.stdin = InputStream.from_bytes(prev_output)

                # Execute the process
                process.execute()

            # Get output from last process
            output = processes[-1].get_stdout()
            output_str = output.decode('utf-8', errors='replace')
            # Only remove trailing newline (not all whitespace)
            if output_str.endswith('\n'):
                output_str = output_str[:-1]
            return output_str
        except Exception as e:
            return ''

    def _strip_comment(self, line: str) -> str:
        """
        Remove comments from a command line
        - Lines starting with # are treated as full comments
        - Inline comments (# after command) are removed
        - Comment markers inside quotes are preserved

        Uses the robust lexer module for consistent parsing.

        Args:
            line: Command line string

        Returns:
            Line with comments removed
        """
        from .lexer import strip_comments

        # Empty line check
        if not line.lstrip():
            return ''

        # Strip # comments using lexer (respects quotes)
        return strip_comments(line, comment_chars='#')

    def _get_variable(self, var_name: str) -> str:
        """
        Get variable value, checking local scopes first, then global env

        Args:
            var_name: Variable name

        Returns:
            Variable value or empty string if not found
        """
        # Check if we're in a function and have a local variable
        if self.env.get('_function_depth'):
            local_key = f'_local_{var_name}'
            if local_key in self.env:
                return self.env[local_key]

        # Check local scopes from innermost to outermost
        for scope in reversed(self.local_scopes):
            if var_name in scope:
                return scope[var_name]

        # Fall back to global env
        return self.env.get(var_name, '')

    def _set_variable(self, var_name: str, value: str, local: bool = False):
        """
        Set variable value

        Args:
            var_name: Variable name
            value: Variable value
            local: If True, set in current local scope; otherwise set in global env
        """
        if local and self.local_scopes:
            # Set in current local scope
            self.local_scopes[-1][var_name] = value
            # Also set in env with _local_ prefix for compatibility
            self.env[f'_local_{var_name}'] = value
        elif self.env.get('_function_depth') and f'_local_{var_name}' in self.env:
            # We're in a function and this variable was declared local
            # Update the local variable, not the global one
            self.env[f'_local_{var_name}'] = value
        else:
            # Set in global env
            self.env[var_name] = value

    def _expand_basic_variables(self, text: str) -> str:
        """
        Core variable expansion logic (shared by all expansion methods)

        Expands:
        - Special variables: $?, $#, $@, $0
        - Braced variables: ${VAR}
        - Positional parameters: $1, $2, ...
        - Simple variables: $VAR

        Does NOT expand:
        - Arithmetic: $((expr))
        - Command substitution: $(cmd), `cmd`

        Args:
            text: Text containing variable references

        Returns:
            Text with variables expanded
        """
        import re

        # First, expand special variables (in specific order to avoid conflicts)
        text = text.replace('$?', self._get_variable('?'))
        text = text.replace('$#', self._get_variable('#'))
        text = text.replace('$@', self._get_variable('@'))
        text = text.replace('$0', self._get_variable('0'))

        # Expand ${VAR}
        def replace_braced(match):
            var_name = match.group(1)
            return self._get_variable(var_name)

        text = re.sub(r'\$\{([A-Za-z_][A-Za-z0-9_]*|\d+)\}', replace_braced, text)

        # Expand $1, $2, etc.
        def replace_positional(match):
            var_name = match.group(1)
            return self._get_variable(var_name)

        text = re.sub(r'\$(\d+)', replace_positional, text)

        # Expand $VAR
        def replace_simple(match):
            var_name = match.group(1)
            return self._get_variable(var_name)

        text = re.sub(r'\$([A-Za-z_][A-Za-z0-9_]*)', replace_simple, text)

        return text

    def _expand_variables_without_command_sub(self, text: str) -> str:
        """
        Expand environment variables but NOT command substitutions
        Used in command substitution to avoid infinite recursion

        This is now a thin wrapper around _expand_basic_variables()
        """
        return self._expand_basic_variables(text)

    def _safe_eval_arithmetic(self, expr: str) -> int:
        """
        Safely evaluate an arithmetic expression without using eval()

        Supports: +, -, *, /, %, ** (power), and parentheses
        Only allows integers and these operators - no function calls or imports

        Args:
            expr: Arithmetic expression string (e.g., "5 + 3 * 2")

        Returns:
            Integer result of evaluation
        """
        import ast
        import operator

        # Map of allowed operators
        ALLOWED_OPS = {
            ast.Add: operator.add,
            ast.Sub: operator.sub,
            ast.Mult: operator.mul,
            ast.FloorDiv: operator.floordiv,  # // operator
            ast.Div: operator.truediv,        # / operator
            ast.Mod: operator.mod,
            ast.Pow: operator.pow,
            ast.USub: operator.neg,           # Unary minus
            ast.UAdd: operator.pos,           # Unary plus
        }

        def eval_node(node):
            """Recursively evaluate AST nodes"""
            if isinstance(node, ast.Constant):
                # Python 3.8+ uses ast.Constant for numbers
                if isinstance(node.value, (int, float)):
                    return node.value
                else:
                    raise ValueError(f"Only numeric constants allowed, got {type(node.value)}")
            elif hasattr(ast, 'Num') and isinstance(node, ast.Num):
                # Python 3.7 and earlier use ast.Num (removed in Python 3.12)
                return node.n
            elif isinstance(node, ast.BinOp):
                # Binary operation (e.g., 5 + 3)
                if type(node.op) not in ALLOWED_OPS:
                    raise ValueError(f"Operator {type(node.op).__name__} not allowed")
                left = eval_node(node.left)
                right = eval_node(node.right)
                return ALLOWED_OPS[type(node.op)](left, right)
            elif isinstance(node, ast.UnaryOp):
                # Unary operation (e.g., -5)
                if type(node.op) not in ALLOWED_OPS:
                    raise ValueError(f"Operator {type(node.op).__name__} not allowed")
                operand = eval_node(node.operand)
                return ALLOWED_OPS[type(node.op)](operand)
            else:
                raise ValueError(f"Node type {type(node).__name__} not allowed")

        try:
            # Strip whitespace before parsing
            expr = expr.strip()

            # Parse the expression into an AST
            tree = ast.parse(expr, mode='eval')

            # Evaluate the AST safely
            result = eval_node(tree.body)

            # Return as integer (bash arithmetic uses integers)
            return int(result)
        except (SyntaxError, ValueError, ZeroDivisionError) as e:
            # If evaluation fails, return 0 (bash behavior)
            return 0
        except Exception:
            # Catch any unexpected errors and return 0
            return 0

    def _expand_variables(self, text: str) -> str:
        """
        Expand ALL variable types and command substitutions

        Uses the new ExpressionExpander for unified handling of:
        - Special variables: $?, $#, $@, $0
        - Simple variables: $VAR
        - Braced variables: ${VAR}, ${VAR:-default}, ${VAR#pattern}, etc.
        - Positional parameters: $1, $2, ...
        - Arithmetic expressions: $((expr))
        - Command substitution: $(command), `command`

        Returns:
            Text with all expansions applied
        """
        return self.expression_expander.expand(text)

    def _expand_variables_legacy(self, text: str) -> str:
        """
        Legacy implementation of variable expansion.
        Kept for reference and fallback if needed.
        """
        import re

        # Step 1: Expand command substitutions FIRST: $(command) and `command`
        # This must be done BEFORE arithmetic to allow $(cmd) inside $((arithmetic))
        def replace_command_subst(command):
            """Execute a command substitution and return its output"""
            return self._execute_command_substitution(command)

        def find_innermost_command_subst(text, start_pos=0):
            """
            Find the position of the innermost $(command) substitution.
            Returns (start, end, command) or None if no substitution found.
            """
            i = start_pos
            while i < len(text) - 1:
                if text[i:i+2] == '$(':
                    # Check if this is $((
                    if i < len(text) - 2 and text[i:i+3] == '$((':
                        i += 1
                        continue

                    # Found a $( - scan to find matching )
                    start = i
                    i += 2
                    depth = 1
                    cmd_start = i

                    in_single_quote = False
                    in_double_quote = False
                    escape_next = False
                    has_nested = False

                    while i < len(text) and depth > 0:
                        char = text[i]

                        if escape_next:
                            escape_next = False
                            i += 1
                            continue

                        if char == '\\':
                            escape_next = True
                            i += 1
                            continue

                        if char == '"' and not in_single_quote:
                            in_double_quote = not in_double_quote
                        elif char == "'" and not in_double_quote:
                            in_single_quote = not in_single_quote
                        elif not in_single_quote and not in_double_quote:
                            # Check for nested $(
                            if i < len(text) - 1 and text[i:i+2] == '$(':
                                if i >= len(text) - 2 or text[i:i+3] != '$((':
                                    has_nested = True

                            if char == '(':
                                depth += 1
                            elif char == ')':
                                depth -= 1

                        i += 1

                    if depth == 0:
                        command = text[cmd_start:i-1]

                        # If this has nested substitutions, recurse to find the innermost
                        if has_nested:
                            nested_result = find_innermost_command_subst(text, cmd_start)
                            if nested_result:
                                return nested_result

                        # This is innermost (no nested substitutions)
                        return (start, i, command)
                else:
                    i += 1

            return None

        def find_and_replace_command_subst(text):
            """
            Find and replace $(command) patterns, processing from innermost to outermost
            """
            max_iterations = 10
            for iteration in range(max_iterations):
                result = find_innermost_command_subst(text)

                if result is None:
                    # No more substitutions
                    break

                start, end, command = result
                replacement = replace_command_subst(command)
                text = text[:start] + replacement + text[end:]

            return text

        text = find_and_replace_command_subst(text)

        # Process `...` command substitution (backticks)
        def replace_backtick_subst(match):
            command = match.group(1)
            return self._execute_command_substitution(command)

        text = re.sub(r'`([^`]+)`', replace_backtick_subst, text)

        # Step 2: Expand arithmetic expressions $((expr))
        # This is done AFTER command substitution to allow $(cmd) inside arithmetic
        def replace_arithmetic(match):
            expr = match.group(1)
            try:
                # Expand variables in the expression
                # In bash arithmetic, variables can be used with or without $
                # We need to expand both $VAR and VAR
                expanded_expr = expr

                # First, expand ${VAR} and ${N} (braced form) - including positional params
                for var_match in re.finditer(r'\$\{([A-Za-z_][A-Za-z0-9_]*|\d+)\}', expr):
                    var_name = var_match.group(1)
                    var_value = self._get_variable(var_name) or '0'
                    try:
                        int(var_value)
                    except ValueError:
                        var_value = '0'
                    expanded_expr = expanded_expr.replace(f'${{{var_name}}}', var_value)

                # Then expand $VAR and $N (non-braced form)
                for var_match in re.finditer(r'\$([A-Za-z_][A-Za-z0-9_]*|\d+)', expanded_expr):
                    var_name = var_match.group(1)
                    var_value = self._get_variable(var_name) or '0'
                    # Try to convert to int, default to 0 if not numeric
                    try:
                        int(var_value)
                    except ValueError:
                        var_value = '0'
                    expanded_expr = expanded_expr.replace(f'${var_name}', var_value)

                # Then, expand VAR (without dollar sign)
                # We need to be careful not to replace keywords like 'and', 'or', 'not'
                # and not to replace numbers
                for var_match in re.finditer(r'\b([A-Za-z_][A-Za-z0-9_]*)\b', expanded_expr):
                    var_name = var_match.group(1)
                    # Skip Python keywords
                    if var_name in ['and', 'or', 'not', 'in', 'is']:
                        continue
                    # Check if variable exists (in local or global scope)
                    var_value = self._get_variable(var_name)
                    if var_value:
                        # Try to convert to int, default to 0 if not numeric
                        try:
                            int(var_value)
                        except ValueError:
                            var_value = '0'
                        expanded_expr = expanded_expr.replace(var_name, var_value)

                # Safely evaluate the arithmetic expression using AST parser
                # This replaces the dangerous eval() call with a secure alternative
                result = self._safe_eval_arithmetic(expanded_expr)
                return str(result)
            except Exception as e:
                # If evaluation fails, return 0
                return '0'

        # Use a more sophisticated pattern to handle nested parentheses
        # Match $((anything)) where we need to count parentheses properly
        def find_and_replace_arithmetic(text):
            result = []
            i = 0
            while i < len(text):
                # Look for $((
                if i < len(text) - 2 and text[i:i+3] == '$((':
                    # Found start of arithmetic expression
                    start = i
                    i += 3
                    depth = 2  # We've seen $(( which is 2 open parens
                    expr_start = i

                    # Find the matching ))
                    while i < len(text) and depth > 0:
                        if text[i] == '(':
                            depth += 1
                        elif text[i] == ')':
                            depth -= 1
                        i += 1

                    if depth == 0:
                        # Found matching ))
                        expr = text[expr_start:i-2]  # -2 to exclude the ))
                        # Create a match object-like thing
                        class FakeMatch:
                            def __init__(self, expr):
                                self.expr = expr
                            def group(self, n):
                                return self.expr
                        replacement = replace_arithmetic(FakeMatch(expr))
                        result.append(replacement)
                    else:
                        # Unmatched, keep original
                        result.append(text[start:i])
                else:
                    result.append(text[i])
                    i += 1
            return ''.join(result)

        text = find_and_replace_arithmetic(text)

        # Step 3: Expand basic variables ($VAR, ${VAR}, $1, etc.)
        # Use shared expansion logic to avoid code duplication
        text = self._expand_basic_variables(text)

        return text

    def _expand_globs(self, commands):
        """
        Expand glob patterns in command arguments

        Args:
            commands: List of (cmd, args) tuples

        Returns:
            List of (cmd, expanded_args) tuples
        """
        import fnmatch

        expanded_commands = []

        for cmd, args in commands:
            expanded_args = []

            for arg in args:
                # Skip flags (arguments starting with -)
                if arg.startswith('-'):
                    expanded_args.append(arg)
                # Check if argument contains glob characters
                elif '*' in arg or '?' in arg or '[' in arg:
                    # Try to expand the glob pattern
                    matches = self._match_glob_pattern(arg)

                    if matches:
                        # Expand to matching files
                        expanded_args.extend(sorted(matches))
                    else:
                        # No matches, keep original pattern
                        expanded_args.append(arg)
                else:
                    # Not a glob pattern, keep as is
                    expanded_args.append(arg)

            expanded_commands.append((cmd, expanded_args))

        return expanded_commands

    def _match_glob_pattern(self, pattern: str):
        """
        Match a glob pattern against files in the filesystem

        Args:
            pattern: Glob pattern (e.g., "*.txt", "/local/*.log")

        Returns:
            List of matching file paths
        """
        import fnmatch
        import os

        # Resolve the pattern to absolute path
        if pattern.startswith('/'):
            # Absolute pattern
            dir_path = os.path.dirname(pattern) or '/'
            file_pattern = os.path.basename(pattern)
        else:
            # Relative pattern
            dir_path = self.cwd
            file_pattern = pattern

        matches = []

        try:
            # List files in the directory
            entries = self.filesystem.list_directory(dir_path)

            for entry in entries:
                # Match against pattern
                if fnmatch.fnmatch(entry['name'], file_pattern):
                    # Build full path
                    if dir_path == '/':
                        full_path = '/' + entry['name']
                    else:
                        full_path = dir_path + '/' + entry['name']

                    matches.append(full_path)
        except Exception as e:
            # Directory doesn't exist or other error
            # Return empty list to keep original pattern
            pass

        return matches

    def _needs_more_input(self, line: str) -> bool:
        """
        Check if the line needs more input (multiline continuation)

        Returns True if:
        - Line ends with backslash \
        - Unclosed quotes (single or double)
        - Unclosed brackets/parentheses
        """
        # Check for backslash continuation
        if line.rstrip().endswith('\\'):
            return True

        # Check for unclosed quotes
        in_single_quote = False
        in_double_quote = False
        escape_next = False

        for char in line:
            if escape_next:
                escape_next = False
                continue

            if char == '\\':
                escape_next = True
                continue

            if char == '"' and not in_single_quote:
                in_double_quote = not in_double_quote
            elif char == "'" and not in_double_quote:
                in_single_quote = not in_single_quote

        if in_single_quote or in_double_quote:
            return True

        # Check for unclosed brackets/parentheses
        bracket_count = 0
        paren_count = 0

        for char in line:
            if char == '(':
                paren_count += 1
            elif char == ')':
                paren_count -= 1
            elif char == '{':
                bracket_count += 1
            elif char == '}':
                bracket_count -= 1

        if bracket_count > 0 or paren_count > 0:
            return True

        return False

    def resolve_path(self, path: str) -> str:
        """
        Resolve a relative or absolute path to an absolute path

        Args:
            path: Path to resolve (can be relative or absolute)

        Returns:
            Absolute path
        """
        if not path:
            return self.cwd

        # Already absolute
        if path.startswith('/'):
            # Normalize the path (remove redundant slashes, handle . and ..)
            return os.path.normpath(path)

        # Relative path - join with cwd
        full_path = os.path.join(self.cwd, path)
        # Normalize to handle . and ..
        return os.path.normpath(full_path)

    def execute_for_loop(self, lines: List[str]) -> int:
        """
        Execute a for/do/done loop

        Args:
            lines: List of lines making up the for loop

        Returns:
            Exit code of last executed command
        """
        parsed = self.control_parser.parse_for_loop(lines)

        if not parsed:
            self.console.print("[red]Syntax error: invalid for loop syntax[/red]", highlight=False)
            self.console.print("[yellow]Expected: for var in items; do commands; done[/yellow]", highlight=False)
            return 1

        try:
            return self.executor.execute_for(parsed)
        except BreakException:
            # Break at top level - should not happen normally
            return 0
        except ContinueException:
            # Continue at top level - should not happen normally
            return 0

    def execute_while_loop(self, lines: List[str]) -> int:
        """
        Execute a while/do/done loop

        Args:
            lines: List of lines making up the while loop

        Returns:
            Exit code of last executed command
        """
        parsed = self.control_parser.parse_while_loop(lines)

        if not parsed:
            self.console.print("[red]Syntax error: invalid while loop syntax[/red]", highlight=False)
            self.console.print("[yellow]Expected: while condition; do commands; done[/yellow]", highlight=False)
            return 1

        try:
            return self.executor.execute_while(parsed)
        except BreakException:
            # Break at top level - should not happen normally
            return 0
        except ContinueException:
            # Continue at top level - should not happen normally
            return 0

    def _parse_for_loop(self, lines: List[str]) -> dict:
        """
        Parse a for/in/do/done loop from a list of lines

        Returns:
            Dict with structure: {
                'var': variable_name,
                'items': [list of items],
                'commands': [list of commands]
            }
        """
        result = {
            'var': None,
            'items': [],
            'commands': []
        }

        state = 'for'  # States: 'for', 'do'
        first_for_parsed = False  # Track if we've parsed the first for statement

        i = 0
        while i < len(lines):
            line = lines[i].strip()
            i += 1

            if not line or line.startswith('#'):
                continue

            # Strip comments before checking keywords
            line_no_comment = self._strip_comment(line).strip()

            if line_no_comment == 'done':
                # End of for loop
                break
            elif line_no_comment == 'do':
                state = 'do'
            elif line_no_comment.startswith('do '):
                # 'do' with command on same line
                state = 'do'
                cmd_after_do = line_no_comment[3:].strip()
                if cmd_after_do:
                    result['commands'].append(cmd_after_do)
            elif line_no_comment.startswith('for '):
                # Only parse the FIRST for statement
                # Nested for loops should be treated as commands
                if not first_for_parsed:
                    # Parse: for var in item1 item2 item3
                    # or: for var in item1 item2 item3; do
                    parts = line_no_comment[4:].strip()

                    # Remove trailing '; do' or 'do' if present
                    if parts.endswith('; do'):
                        parts = parts[:-4].strip()
                        state = 'do'
                    elif parts.endswith(' do'):
                        parts = parts[:-3].strip()
                        state = 'do'

                    # Split by 'in' keyword
                    if ' in ' in parts:
                        var_and_in = parts.split(' in ', 1)
                        result['var'] = var_and_in[0].strip()
                        items_str = var_and_in[1].strip()

                        # Remove inline comments before processing
                        items_str = self._strip_comment(items_str)

                        # Expand variables in items string first
                        items_str = self._expand_variables(items_str)

                        # Split items by whitespace
                        # Use simple split() for word splitting after variable expansion
                        # This mimics bash's word splitting behavior
                        raw_items = items_str.split()

                        # Expand glob patterns in each item
                        expanded_items = []
                        for item in raw_items:
                            # Check if item contains glob characters
                            if '*' in item or '?' in item or '[' in item:
                                # Try to expand the glob pattern
                                matches = self._match_glob_pattern(item)
                                if matches:
                                    # Add all matching files
                                    expanded_items.extend(sorted(matches))
                                else:
                                    # No matches, keep original pattern
                                    expanded_items.append(item)
                            else:
                                # Not a glob pattern, keep as is
                                expanded_items.append(item)

                        result['items'] = expanded_items
                        first_for_parsed = True
                    else:
                        # Invalid for syntax
                        return None
                else:
                    # This is a nested for loop - collect it as a single command block
                    if state == 'do':
                        result['commands'].append(line)
                        # Now collect the rest of the nested loop (do...done)
                        while i < len(lines):
                            nested_line = lines[i].strip()
                            result['commands'].append(nested_line)
                            # Strip comments before checking for 'done'
                            nested_line_no_comment = self._strip_comment(nested_line).strip()
                            if nested_line_no_comment == 'done':
                                break
                            i += 1
            else:
                # Regular command in loop body
                if state == 'do':
                    result['commands'].append(line)
                elif state == 'for' and first_for_parsed:
                    # We're in 'for' state after parsing the for statement,
                    # but seeing a regular command before 'do' - this is a syntax error
                    return None

        # Validate the parsed result
        # Must have: variable name, items, and at least reached 'do' state
        if not result['var']:
            return None

        return result

    def _parse_while_loop(self, lines: List[str]) -> dict:
        """
        Parse a while/do/done loop from a list of lines

        Returns:
            Dict with structure: {
                'condition': condition_command,
                'commands': [list of commands]
            }
        """
        result = {
            'condition': None,
            'commands': []
        }

        state = 'while'  # States: 'while', 'do'
        first_while_parsed = False  # Track if we've parsed the first while statement

        i = 0
        while i < len(lines):
            line = lines[i].strip()
            i += 1

            if not line or line.startswith('#'):
                continue

            # Strip comments before checking keywords
            line_no_comment = self._strip_comment(line).strip()

            if line_no_comment == 'done':
                # End of while loop
                break
            elif line_no_comment == 'do':
                state = 'do'
            elif line_no_comment.startswith('do '):
                # 'do' with command on same line
                state = 'do'
                cmd_after_do = line_no_comment[3:].strip()
                if cmd_after_do:
                    result['commands'].append(cmd_after_do)
            elif line_no_comment.startswith('while '):
                # Only parse the FIRST while statement
                # Nested while loops should be treated as commands
                if not first_while_parsed:
                    # Parse: while condition
                    # or: while condition; do
                    condition = line_no_comment[6:].strip()

                    # Remove trailing '; do' or 'do' if present
                    if condition.endswith('; do'):
                        condition = condition[:-4].strip()
                        state = 'do'
                    elif condition.endswith(' do'):
                        condition = condition[:-3].strip()
                        state = 'do'

                    # Remove inline comments from condition
                    condition = self._strip_comment(condition)

                    result['condition'] = condition
                    first_while_parsed = True
                else:
                    # This is a nested while loop - collect it as a command
                    if state == 'do':
                        result['commands'].append(line)
                        # Now collect the rest of the nested loop (do...done)
                        while i < len(lines):
                            nested_line = lines[i].strip()
                            result['commands'].append(nested_line)
                            # Strip comments before checking for 'done'
                            nested_line_no_comment = self._strip_comment(nested_line).strip()
                            if nested_line_no_comment == 'done':
                                break
                            i += 1
            else:
                # Regular command in loop body
                if state == 'do':
                    result['commands'].append(line)
                elif state == 'while' and first_while_parsed:
                    # We're in 'while' state after parsing the while statement,
                    # but seeing a regular command before 'do' - this is a syntax error
                    return None

        # Validate the parsed result
        # Must have: condition and at least reached 'do' state
        if not result['condition']:
            return None

        return result

    def execute_if_statement(self, lines: List[str]) -> int:
        """
        Execute an if/then/else/fi statement

        Args:
            lines: List of lines making up the if statement

        Returns:
            Exit code of executed commands
        """
        parsed = self.control_parser.parse_if_statement(lines)

        # Check if parsing was successful
        if not parsed or not parsed.branches:
            self.console.print("[red]Syntax error: invalid if statement syntax[/red]", highlight=False)
            self.console.print("[yellow]Expected: if condition; then commands; fi[/yellow]", highlight=False)
            return 1

        # Execute using the new executor - exceptions will propagate
        return self.executor.execute_if(parsed)

    def _parse_if_statement(self, lines: List[str]) -> dict:
        """
        Parse an if/then/else/fi statement from a list of lines

        Returns:
            Dict with structure: {
                'conditions': [(condition_cmd, commands_block), ...],
                'else_block': [commands] or None
            }
        """
        result = {
            'conditions': [],
            'else_block': None
        }

        current_block = []
        current_condition = None
        state = 'if'  # States: 'if', 'then', 'elif', 'else'

        for line in lines:
            line = line.strip()

            if not line or line.startswith('#'):
                continue

            if line == 'fi':
                # End of if statement
                if state == 'then' and current_condition is not None:
                    result['conditions'].append((current_condition, current_block))
                elif state == 'else':
                    result['else_block'] = current_block
                break
            elif line == 'then':
                state = 'then'
                current_block = []
            elif line.startswith('then '):
                # 'then' with command on same line (e.g., "then echo foo")
                state = 'then'
                current_block = []
                # Extract command after 'then'
                cmd_after_then = line[5:].strip()
                if cmd_after_then:
                    current_block.append(cmd_after_then)
            elif line.startswith('elif '):
                # Save previous condition block
                if current_condition is not None:
                    result['conditions'].append((current_condition, current_block))
                # Start new condition
                condition_part = line[5:].strip()
                # Remove inline comments before processing
                condition_part = self._strip_comment(condition_part)
                # Check if 'then' is on the same line
                has_then = condition_part.endswith(' then')
                # Remove trailing 'then' if present on same line
                if has_then:
                    condition_part = condition_part[:-5].strip()
                current_condition = condition_part.rstrip(';')
                # If 'then' was on same line, move to 'then' state
                state = 'then' if has_then else 'if'
                current_block = []
            elif line == 'else':
                # Save previous condition block
                if current_condition is not None:
                    result['conditions'].append((current_condition, current_block))
                state = 'else'
                current_block = []
                current_condition = None
            elif line.startswith('else '):
                # 'else' with command on same line
                if current_condition is not None:
                    result['conditions'].append((current_condition, current_block))
                state = 'else'
                current_block = []
                current_condition = None
                # Extract command after 'else'
                cmd_after_else = line[5:].strip()
                if cmd_after_else:
                    current_block.append(cmd_after_else)
            elif line.startswith('if '):
                # Initial if statement - extract condition
                condition_part = line[3:].strip()
                # Remove inline comments before processing
                condition_part = self._strip_comment(condition_part)
                # Check if 'then' is on the same line
                has_then = condition_part.endswith(' then')
                # Remove trailing 'then' if present on same line
                if has_then:
                    condition_part = condition_part[:-5].strip()
                current_condition = condition_part.rstrip(';')
                # If 'then' was on same line, move to 'then' state
                state = 'then' if has_then else 'if'
                if has_then:
                    current_block = []
            else:
                # Regular command in current block
                if state == 'then' or state == 'else':
                    current_block.append(line)

        return result

    def _parse_function_definition(self, lines: List[str]) -> Optional[dict]:
        """
        Parse a function definition from a list of lines

        Syntax:
            function_name() {
                commands
            }

        Or:
            function function_name {
                commands
            }

        Or single-line:
            function_name() { commands; }

        Returns:
            Dict with structure: {
                'name': function_name,
                'body': [list of commands]
            }
        """
        result = {
            'name': None,
            'body': []
        }

        if not lines:
            return None

        first_line = lines[0].strip()

        # Check for single-line function: function_name() { commands... }
        import re
        single_line_match = re.match(r'^([A-Za-z_][A-Za-z0-9_]*)\s*\(\)\s*\{(.+)\}', first_line)
        if not single_line_match:
            single_line_match = re.match(r'^function\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{(.+)\}', first_line)

        if single_line_match:
            # Single-line function
            result['name'] = single_line_match.group(1)
            body = single_line_match.group(2).strip()
            # Split by semicolons to get individual commands
            if ';' in body:
                result['body'] = [cmd.strip() for cmd in body.split(';') if cmd.strip()]
            else:
                result['body'] = [body]
            return result

        # Check for multi-line function_name() { syntax
        match = re.match(r'^([A-Za-z_][A-Za-z0-9_]*)\s*\(\)\s*\{?\s*$', first_line)
        if not match:
            # Check for function function_name { syntax
            match = re.match(r'^function\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{?\s*$', first_line)

        if not match:
            return None

        result['name'] = match.group(1)

        # Collect function body
        # If first line ends with {, start from next line
        # Otherwise, expect { on next line
        start_index = 1
        if not first_line.endswith('{'):
            # Look for opening brace
            if start_index < len(lines) and lines[start_index].strip() == '{':
                start_index += 1

        # Collect lines until closing }
        brace_depth = 1
        for i in range(start_index, len(lines)):
            line = lines[i].strip()

            # Skip comments and empty lines
            if not line or line.startswith('#'):
                continue

            # Check for closing brace
            if line == '}':
                brace_depth -= 1
                if brace_depth == 0:
                    break
            elif '{' in line:
                # Track nested braces
                brace_depth += line.count('{')
                brace_depth -= line.count('}')

            result['body'].append(lines[i])

        return result

    def execute_function(self, func_name: str, args: List[str]) -> int:
        """
        Execute a user-defined function

        Delegates to executor.execute_function_call() which handles:
        - Parameter passing ($1, $2, etc.)
        - Local variable scope
        - Return value handling via ReturnException
        - Proper cleanup on exit

        Args:
            func_name: Function name
            args: Function arguments

        Returns:
            Exit code of function execution
        """
        return self.executor.execute_function_call(func_name, args)

    def execute_script(self, file_path: str, script_args: Optional[List[str]] = None, silent: bool = False) -> Optional[int]:
        """
        Execute a script file from AGFS filesystem line by line.

        Args:
            file_path: Path to script file in AGFS
            script_args: List of arguments to pass to script (accessible as $1, $2, etc.)
            silent: If True, suppress error messages for missing files

        Returns:
            Exit code from script execution, or None if file not found
        """
        # Check if file exists in AGFS
        try:
            if not self.filesystem.file_exists(file_path):
                return None
        except Exception:
            return None

        # Read script content from AGFS
        try:
            content = self.filesystem.read_file(file_path)
            if isinstance(content, bytes):
                content = content.decode('utf-8')
        except Exception as e:
            if not silent:
                sys.stderr.write(f"agfs-shell: {file_path}: {str(e)}\n")
            return 1

        return self.execute_script_content(content, script_name=file_path, script_args=script_args, silent=silent)

    def execute_script_content(self, content: str, script_name: str = "<script>",
                                script_args: Optional[List[str]] = None, silent: bool = False) -> int:
        """
        Execute script content line by line.

        This is the core script execution logic used by:
        - execute_script (AGFS files)
        - execute_script_file (local files via CLI)
        - source command

        Args:
            content: Script content as string
            script_name: Name to use for $0 (default: "<script>")
            script_args: List of arguments to pass to script (accessible as $1, $2, etc.)
            silent: If True, suppress error messages

        Returns:
            Exit code from script execution
        """
        lines = content.splitlines()

        # Save current environment for positional parameters
        old_env = {}
        for key in ['0', '#', '@', '*'] + [str(i) for i in range(1, 100)]:
            if key in self.env:
                old_env[key] = self.env[key]

        # Set script name and arguments as environment variables
        self.env['0'] = script_name

        if script_args:
            for i, arg in enumerate(script_args, start=1):
                self.env[str(i)] = arg
            self.env['#'] = str(len(script_args))
            self.env['@'] = ' '.join(script_args)
            self.env['*'] = ' '.join(script_args)
            # Clear extra positional params from previous invocations
            for i in range(len(script_args) + 1, 100):
                if str(i) in self.env:
                    del self.env[str(i)]
        else:
            self.env['#'] = '0'
            self.env['@'] = ''
            self.env['*'] = ''
            # Clear positional params
            for i in range(1, 100):
                if str(i) in self.env:
                    del self.env[str(i)]

        try:
            exit_code = 0
            i = 0
            while i < len(lines):
                line = lines[i].strip()
                line_num = i + 1

                # Skip empty lines and comments
                if not line or line.startswith('#'):
                    i += 1
                    continue

                # Execute the command
                try:
                    exit_code = self.execute(line)

                    # Check if for-loop needs to be collected
                    if exit_code == EXIT_CODE_FOR_LOOP_NEEDED:
                        for_lines = [line]
                        for_depth = 1
                        i += 1
                        while i < len(lines):
                            next_line = lines[i].strip()
                            for_lines.append(next_line)
                            next_line_no_comment = self._strip_comment(next_line).strip()
                            if next_line_no_comment.startswith('for '):
                                for_depth += 1
                            elif next_line_no_comment == 'done':
                                for_depth -= 1
                                if for_depth == 0:
                                    break
                            i += 1
                        exit_code = self.execute_for_loop(for_lines)
                        if exit_code in [EXIT_CODE_CONTINUE, EXIT_CODE_BREAK]:
                            exit_code = 0

                    elif exit_code == EXIT_CODE_WHILE_LOOP_NEEDED:
                        while_lines = [line]
                        while_depth = 1
                        i += 1
                        while i < len(lines):
                            next_line = lines[i].strip()
                            while_lines.append(next_line)
                            next_line_no_comment = self._strip_comment(next_line).strip()
                            if next_line_no_comment.startswith('while '):
                                while_depth += 1
                            elif next_line_no_comment == 'done':
                                while_depth -= 1
                                if while_depth == 0:
                                    break
                            i += 1
                        exit_code = self.execute_while_loop(while_lines)
                        if exit_code in [EXIT_CODE_CONTINUE, EXIT_CODE_BREAK]:
                            exit_code = 0

                    elif exit_code == EXIT_CODE_FUNCTION_DEF_NEEDED:
                        func_lines = [line]
                        brace_depth = 1
                        i += 1
                        while i < len(lines):
                            next_line = lines[i].strip()
                            func_lines.append(next_line)
                            brace_depth += next_line.count('{')
                            brace_depth -= next_line.count('}')
                            if brace_depth == 0:
                                break
                            i += 1
                        func_ast = self.control_parser.parse_function_definition(func_lines)
                        if func_ast and func_ast.name:
                            self.functions[func_ast.name] = {
                                'name': func_ast.name,
                                'body': func_ast.body,
                                'is_ast': True
                            }
                            exit_code = 0
                        else:
                            sys.stderr.write(f"Error at line {line_num}: invalid function definition\n")
                            return 1

                    elif exit_code == EXIT_CODE_IF_STATEMENT_NEEDED:
                        if_lines = [line]
                        if_depth = 1
                        i += 1
                        while i < len(lines):
                            next_line = lines[i].strip()
                            if_lines.append(next_line)
                            next_line_no_comment = self._strip_comment(next_line).strip()
                            if next_line_no_comment.startswith('if '):
                                if_depth += 1
                            elif next_line_no_comment == 'fi':
                                if_depth -= 1
                                if if_depth == 0:
                                    break
                            i += 1
                        exit_code = self.execute_if_statement(if_lines)

                    self.env['?'] = str(exit_code)
                except SystemExit as e:
                    return e.code if e.code is not None else 0
                except Exception as e:
                    sys.stderr.write(f"Error at line {line_num}: {str(e)}\n")
                    return 1

                i += 1

            return exit_code
        except KeyboardInterrupt:
            sys.stderr.write("\n")
            return 130
        except SystemExit as e:
            return e.code if e.code is not None else 0
        except Exception as e:
            if not silent:
                sys.stderr.write(f"agfs-shell: {script_name}: {str(e)}\n")
            return 1
        finally:
            # Restore old positional parameters
            for key in ['0', '#', '@', '*'] + [str(i) for i in range(1, 100)]:
                if key in self.env and key not in old_env:
                    del self.env[key]
            for key, value in old_env.items():
                self.env[key] = value

    def execute(self, command_line: str, stdin_data: Optional[bytes] = None, heredoc_data: Optional[bytes] = None) -> int:
        """
        Execute a command line (possibly with pipelines and redirections)

        Args:
            command_line: Command string to execute
            stdin_data: Optional stdin data to provide to first command
            heredoc_data: Optional heredoc data (for << redirections)

        Returns:
            Exit code of the pipeline
        """
        # Strip comments from the command line
        command_line = self._strip_comment(command_line)

        # If command is empty after stripping comments, return success
        if not command_line.strip():
            return 0

        # Check for function definition
        import re
        # Match both function_name() { ... } and function function_name { ... }
        func_def_match = re.match(r'^([A-Za-z_][A-Za-z0-9_]*)\s*\(\)\s*\{', command_line.strip())
        if not func_def_match:
            func_def_match = re.match(r'^function\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{', command_line.strip())

        if func_def_match:
            # Check if it's a complete single-line function
            if '}' in command_line:
                # Single-line function definition - use new AST parser
                lines = [command_line]
                func_ast = self.control_parser.parse_function_definition(lines)
                if func_ast and func_ast.name:
                    # Store as AST-based function
                    self.functions[func_ast.name] = {
                        'name': func_ast.name,
                        'body': func_ast.body,
                        'is_ast': True
                    }
                    return 0
                else:
                    self.console.print("[red]Syntax error: invalid function definition[/red]", highlight=False)
                    return 1
            else:
                # Multi-line function - signal to REPL to collect more lines
                return EXIT_CODE_FUNCTION_DEF_NEEDED

        # Also check for function definition without opening brace on first line
        func_def_match2 = re.match(r'^([A-Za-z_][A-Za-z0-9_]*)\s*\(\)\s*$', command_line.strip())
        if not func_def_match2:
            func_def_match2 = re.match(r'^function\s+([A-Za-z_][A-Za-z0-9_]*)\s*$', command_line.strip())

        if func_def_match2:
            # Function definition without opening brace - signal to collect more lines
            return EXIT_CODE_FUNCTION_DEF_NEEDED

        # Check for for loop (special handling required)
        if command_line.strip().startswith('for '):
            # Check if it's a complete single-line for loop
            # Look for 'done' as a separate word/keyword, not as substring
            import re
            if re.search(r'\bdone\b', command_line):
                # Single-line for loop - parse and execute directly
                parts = re.split(r';\s*', command_line)
                lines = [part.strip() for part in parts if part.strip()]
                return self.execute_for_loop(lines)
            else:
                # Multi-line for loop - signal to REPL to collect more lines
                # Return special code to signal for loop collection needed
                return EXIT_CODE_FOR_LOOP_NEEDED

        # Check for while loop (special handling required)
        if command_line.strip().startswith('while '):
            # Check if it's a complete single-line while loop
            # Look for 'done' as a separate word/keyword, not as substring
            import re
            if re.search(r'\bdone\b', command_line):
                # Single-line while loop - parse and execute directly
                parts = re.split(r';\s*', command_line)
                lines = [part.strip() for part in parts if part.strip()]
                return self.execute_while_loop(lines)
            else:
                # Multi-line while loop - signal to REPL to collect more lines
                # Return special code to signal while loop collection needed
                return EXIT_CODE_WHILE_LOOP_NEEDED

        # Check for if statement (special handling required)
        if command_line.strip().startswith('if '):
            # Check if it's a complete single-line if statement
            # Look for 'fi' as a separate word/keyword, not as substring
            import re
            if re.search(r'\bfi\b', command_line):
                # Single-line if statement - parse and execute directly
                # Split by semicolons but preserve the structure
                # Split by '; ' while keeping keywords intact
                parts = re.split(r';\s*', command_line)
                lines = [part.strip() for part in parts if part.strip()]
                return self.execute_if_statement(lines)
            else:
                # Multi-line if statement - signal to REPL to collect more lines
                # Return special code to signal if statement collection needed
                return EXIT_CODE_IF_STATEMENT_NEEDED

        # Check for variable assignment (VAR=value)
        if '=' in command_line and not command_line.strip().startswith('='):
            parts = command_line.split('=', 1)
            if len(parts) == 2:
                var_name = parts[0].strip()
                # Check if it's a valid variable name (not a command with = in args)
                if var_name and var_name.replace('_', '').isalnum() and not ' ' in var_name:
                    var_value = parts[1].strip()

                    # Remove outer quotes if present (both single and double)
                    if len(var_value) >= 2:
                        if (var_value[0] == '"' and var_value[-1] == '"') or \
                           (var_value[0] == "'" and var_value[-1] == "'"):
                            var_value = var_value[1:-1]

                    # Expand variables after removing quotes
                    var_value = self._expand_variables(var_value)
                    self._set_variable(var_name, var_value)
                    return 0

        # Expand variables in command line
        command_line = self._expand_variables(command_line)

        # Handle && and || operators (conditional execution)
        # Split by && and || while preserving which operator was used
        if '&&' in command_line or '||' in command_line:
            # Parse conditional chains: cmd1 && cmd2 || cmd3
            # We need to respect operator precedence and short-circuit evaluation
            parts = []
            operators = []
            current = []
            i = 0
            while i < len(command_line):
                if i < len(command_line) - 1:
                    two_char = command_line[i:i+2]
                    if two_char == '&&' or two_char == '||':
                        parts.append(''.join(current).strip())
                        operators.append(two_char)
                        current = []
                        i += 2
                        continue
                current.append(command_line[i])
                i += 1
            if current:
                parts.append(''.join(current).strip())

            # Execute with short-circuit evaluation
            if parts:
                last_exit_code = self.execute(parts[0], stdin_data=stdin_data, heredoc_data=heredoc_data)
                for i, op in enumerate(operators):
                    if op == '&&':
                        # Execute next only if previous succeeded
                        if last_exit_code == 0:
                            last_exit_code = self.execute(parts[i+1], stdin_data=None, heredoc_data=None)
                        # else: skip execution, keep last_exit_code
                    elif op == '||':
                        # Execute next only if previous failed
                        if last_exit_code != 0:
                            last_exit_code = self.execute(parts[i+1], stdin_data=None, heredoc_data=None)
                        else:
                            # Previous succeeded, set exit code to 0 and don't execute next
                            last_exit_code = 0
                return last_exit_code

        # Parse the command line with redirections
        commands, redirections = self.parser.parse_command_line(command_line)

        # Expand globs in command arguments
        commands = self._expand_globs(commands)

        # If heredoc is detected but no data provided, return special code to signal REPL
        # to read heredoc content
        if 'heredoc_delimiter' in redirections and heredoc_data is None:
            # Return special code to signal that heredoc data is needed
            return EXIT_CODE_HEREDOC_NEEDED

        # If heredoc data is provided, use it as stdin
        if heredoc_data is not None:
            stdin_data = heredoc_data

        if not commands:
            return 0

        # Check if this is a user-defined function call (must be single command, not in pipeline)
        if len(commands) == 1:
            cmd_name, cmd_args = commands[0]
            if cmd_name in self.functions:
                # Execute user-defined function
                return self.execute_function(cmd_name, cmd_args)

        # Special handling for cd command (must be a single command, not in pipeline)
        # Using metadata instead of hardcoded check
        if len(commands) == 1 and CommandMetadata.changes_cwd(commands[0][0]):
            cmd, args = commands[0]
            # Resolve target path
            target = args[0] if args else '/'
            resolved_path = self.resolve_path(target)

            # Verify the directory exists (supports symlinks)
            try:
                # Get file info to check if it's a directory (works with symlinks)
                file_info = self.filesystem.get_file_info(resolved_path)
                is_dir = file_info.get('isDir', False) or file_info.get('type') == 'directory'

                if not is_dir:
                    self.console.print(f"[red]cd: {target}: Not a directory[/red]", highlight=False)
                    return 1

                # It's a directory (or symlink to directory), change to it
                self.cwd = resolved_path
                return 0
            except Exception as e:
                error_msg = str(e)
                if "No such file or directory" in error_msg or "not found" in error_msg.lower():
                    self.console.print(f"[red]cd: {target}: No such file or directory[/red]", highlight=False)
                else:
                    self.console.print(f"[red]cd: {target}: {error_msg}[/red]", highlight=False)
                return 1

        # Resolve paths in redirections
        if 'stdin' in redirections:
            input_file = self.resolve_path(redirections['stdin'])
            try:
                # Use AGFS to read input file
                stdin_data = self.filesystem.read_file(input_file)
            except AGFSClientError as e:
                error_msg = self.filesystem.get_error_message(e)
                self.console.print(f"[red]shell: {error_msg}[/red]", highlight=False)
                return 1
            except Exception as e:
                self.console.print(f"[red]shell: {input_file}: {str(e)}[/red]", highlight=False)
                return 1

        # Build processes for each command
        processes = []
        for i, (cmd, args) in enumerate(commands):
            # Get the executor for this command
            executor = get_builtin(cmd)

            # Resolve relative paths in arguments (for file-related commands)
            # Using metadata instead of hardcoded list
            if CommandMetadata.needs_path_resolution(cmd):
                resolved_args = []
                skip_next = False
                for j, arg in enumerate(args):
                    # Skip if this is a flag value (e.g., the "2" in "-n 2")
                    if skip_next:
                        resolved_args.append(arg)
                        skip_next = False
                        continue

                    # Skip flags (starting with -)
                    if arg.startswith('-'):
                        resolved_args.append(arg)
                        # Check if this flag takes a value (e.g., -n, -L, -d, -f)
                        if arg in ['-n', '-L', '-d', '-f', '-t', '-c'] and j + 1 < len(args):
                            skip_next = True
                        continue

                    # Skip pure numbers (they're likely option values, not paths)
                    try:
                        float(arg)
                        resolved_args.append(arg)
                        continue
                    except ValueError:
                        pass

                    # Resolve path
                    resolved_args.append(self.resolve_path(arg))
                args = resolved_args

            # Create streams
            if i == 0 and stdin_data is not None:
                stdin = InputStream.from_bytes(stdin_data)
            else:
                stdin = InputStream.from_bytes(b'')

            # For streaming output: if no redirections and last command in pipeline,
            # output directly to real stdout for real-time streaming
            if 'stdout' not in redirections and i == len(commands) - 1:
                stdout = OutputStream.from_stdout()
            else:
                stdout = OutputStream.to_buffer()

            stderr = ErrorStream.to_buffer()

            # Create process with filesystem, cwd, env, and shell
            process = Process(
                command=cmd,
                args=args,
                stdin=stdin,
                stdout=stdout,
                stderr=stderr,
                executor=executor,
                filesystem=self.filesystem,
                env=self.env,
                shell=self
            )
            # Pass cwd to process for pwd command
            process.cwd = self.cwd
            processes.append(process)

        # Special case: direct streaming from stdin to file
        # When: single streaming-capable command with no args, stdin from pipe, output to file
        # Implementation: Loop and write chunks (like agfs-shell's write --stream)
        # Using metadata instead of hardcoded check for 'cat'
        if ('stdout' in redirections and
            len(processes) == 1 and
            CommandMetadata.supports_streaming(processes[0].command) and
            not processes[0].args and
            stdin_data is None):

            output_file = self.resolve_path(redirections['stdout'])
            mode = redirections.get('stdout_mode', 'write')

            try:
                # Streaming write: read chunks and write each one separately
                # This enables true streaming (each chunk sent immediately to server)
                chunk_size = 8192  # 8KB chunks
                total_bytes = 0
                is_first_chunk = True
                write_response = None

                while True:
                    chunk = sys.stdin.buffer.read(chunk_size)
                    if not chunk:
                        break

                    # First chunk: overwrite or append based on mode
                    # Subsequent chunks: always append
                    append = (mode == 'append') or (not is_first_chunk)

                    # Write chunk immediately (separate HTTP request per chunk)
                    write_response = self.filesystem.write_file(output_file, chunk, append=append)
                    total_bytes += len(chunk)
                    is_first_chunk = False

                exit_code = 0
                stderr_data = b''
            except AGFSClientError as e:
                error_msg = self.filesystem.get_error_message(e)
                self.console.print(f"[red]shell: {error_msg}[/red]", highlight=False)
                return 1
            except Exception as e:
                self.console.print(f"[red]shell: {output_file}: {str(e)}[/red]", highlight=False)
                return 1
        else:
            # Normal execution path
            pipeline = Pipeline(processes)
            exit_code = pipeline.execute()

            # Get results
            stdout_data = pipeline.get_stdout()
            stderr_data = pipeline.get_stderr()

            # Handle output redirection (>)
            if 'stdout' in redirections:
                output_file = self.resolve_path(redirections['stdout'])
                mode = redirections.get('stdout_mode', 'write')
                append = (mode == 'append')
                try:
                    # Use AGFS to write output file
                    self.filesystem.write_file(output_file, stdout_data, append=append)
                except AGFSClientError as e:
                    error_msg = self.filesystem.get_error_message(e)
                    self.console.print(f"[red]shell: {error_msg}[/red]", highlight=False)
                    return 1
                except Exception as e:
                    self.console.print(f"[red]shell: {output_file}: {str(e)}[/red]", highlight=False)
                    return 1

        # Output handling
        if 'stdout' not in redirections:
            # Check if we need to add a newline
            # Get the last process to check if output ended with newline
            last_process = processes[-1] if processes else None

            # Only output if we used buffered output (not direct stdout)
            # When using OutputStream.from_stdout(), data was already written directly
            if stdout_data:
                try:
                    # Decode and use rich console for output
                    text = stdout_data.decode('utf-8', errors='replace')
                    self.console.print(text, end='', highlight=False)
                    # Ensure output ends with newline (only in interactive mode)
                    if self.interactive and text and not text.endswith('\n'):
                        self.console.print(highlight=False)
                except Exception:
                    # Fallback to raw output if decoding fails
                    sys.stdout.buffer.write(stdout_data)
                    sys.stdout.buffer.flush()
                    # Ensure output ends with newline (only in interactive mode)
                    if self.interactive and stdout_data and not stdout_data.endswith(b'\n'):
                        sys.stdout.write('\n')
                        sys.stdout.flush()
            elif last_process and hasattr(last_process.stdout, 'ends_with_newline'):
                # When using from_stdout() (direct output), check if we need newline (only in interactive mode)
                if self.interactive and not last_process.stdout.ends_with_newline():
                    sys.stdout.write('\n')
                    sys.stdout.flush()

        # Handle error redirection (2>)
        if 'stderr' in redirections:
            error_file = self.resolve_path(redirections['stderr'])
            mode = redirections.get('stderr_mode', 'write')
            append = (mode == 'append')
            try:
                # Use AGFS to write error file
                write_response = self.filesystem.write_file(error_file, stderr_data, append=append)
                # Display write response if it contains data
                if write_response and write_response != "OK":
                    self.console.print(write_response, highlight=False)
            except AGFSClientError as e:
                error_msg = self.filesystem.get_error_message(e)
                self.console.print(f"[red]shell: {error_msg}[/red]", highlight=False)
                return 1
            except Exception as e:
                self.console.print(f"[red]shell: {error_file}: {str(e)}[/red]", highlight=False)
                return 1
        else:
            # Output to stderr if no redirection
            if stderr_data:
                try:
                    # Decode and use rich console for stderr
                    text = stderr_data.decode('utf-8', errors='replace')
                    self.console.print(f"[red]{text}[/red]", end='', highlight=False)
                except Exception:
                    # Fallback to raw output
                    sys.stderr.buffer.write(stderr_data)
                    sys.stderr.buffer.flush()

        return exit_code

    def repl(self):
        """Run interactive REPL"""
        # Set interactive mode flag
        self.interactive = True
        self.console.print("""     __  __ __ 
 /\\ / _ |_ (_  
/--\\\\__)|  __) 
        """)
        self.console.print(f"[bold cyan]agfs-shell[/bold cyan] v{__version__}", highlight=False)

        # Check server connection - exit if failed
        if not self.filesystem.check_connection():
            self.console.print(f"[red]Error: Cannot connect to AGFS server at {self.server_url}[/red]", highlight=False)
            self.console.print("Make sure the server is running.", highlight=False)
            sys.exit(1)

        self.console.print(f"Connected to AGFS server at [green]{self.server_url}[/green]", highlight=False)
        self.console.print("Type [cyan]'help'[/cyan] for help, [cyan]Ctrl+D[/cyan] or [cyan]'exit'[/cyan] to quit", highlight=False)
        self.console.print(highlight=False)

        # Setup tab completion and history
        history_loaded = False
        try:
            import readline
            import os
            from .completer import ShellCompleter

            completer = ShellCompleter(self.filesystem)
            # Pass shell reference to completer for cwd
            completer.shell = self
            readline.set_completer(completer.complete)

            # Set up completion display hook for better formatting
            try:
                # Try to set display matches hook (GNU readline only)
                def display_matches(substitution, matches, longest_match_length):
                    """Display completion matches in a clean format"""
                    # Print newline before matches
                    print()

                    # Display matches in columns
                    if len(matches) <= 10:
                        # Few matches - display in a single column
                        for match in matches:
                            print(f"  {match}")
                    else:
                        # Many matches - display in multiple columns
                        import shutil
                        term_width = shutil.get_terminal_size((80, 20)).columns
                        col_width = longest_match_length + 2
                        num_cols = max(1, term_width // col_width)

                        for i, match in enumerate(matches):
                            print(f"  {match:<{col_width}}", end='')
                            if (i + 1) % num_cols == 0:
                                print()
                        print()

                    # Re-display prompt
                    prompt = f"agfs:{self.cwd}> "
                    print(prompt + readline.get_line_buffer(), end='', flush=True)

                readline.set_completion_display_matches_hook(display_matches)
            except AttributeError:
                # libedit doesn't support display matches hook
                pass

            # Different binding for libedit (macOS) vs GNU readline (Linux)
            if 'libedit' in readline.__doc__:
                # macOS/BSD libedit
                readline.parse_and_bind("bind ^I rl_complete")
                # Set completion display to show candidates properly
                readline.parse_and_bind("set show-all-if-ambiguous on")
                readline.parse_and_bind("set completion-display-width 0")
            else:
                # GNU readline
                readline.parse_and_bind("tab: complete")
                # Better completion display
                readline.parse_and_bind("set show-all-if-ambiguous on")
                readline.parse_and_bind("set completion-display-width 0")

            # Configure readline to use space and special chars as delimiters
            # This allows path completion to work properly
            readline.set_completer_delims(' \t\n;|&<>()')

            # Setup history
            # History file location: use HISTFILE variable (modifiable via export command)
            # Default: $HOME/.agfs_shell_history
            history_file = os.path.expanduser(self.env.get('HISTFILE', '~/.agfs_shell_history'))

            # Set history length
            readline.set_history_length(1000)

            # Try to load existing history
            try:
                readline.read_history_file(history_file)
                history_loaded = True
            except FileNotFoundError:
                # History file doesn't exist yet - will be created on exit
                pass
            except Exception as e:
                # Other errors - warn but continue
                self.console.print(f"[yellow]Warning: Could not load history: {e}[/yellow]", highlight=False)

        except ImportError:
            # readline not available (e.g., on Windows without pyreadline)
            pass

        while self.running:
            try:
                # Read command (possibly multiline)
                try:
                    # Primary prompt
                    prompt = f"agfs:{self.cwd}> "
                    line = input(prompt)

                    # Start building the command
                    self.multiline_buffer = [line]

                    # Check if we need more input
                    while self._needs_more_input(' '.join(self.multiline_buffer)):
                        # Secondary prompt (like bash PS2)
                        continuation_prompt = "> "
                        try:
                            next_line = input(continuation_prompt)
                            self.multiline_buffer.append(next_line)
                        except EOFError:
                            # Ctrl+D during continuation - cancel multiline
                            self.console.print(highlight=False)
                            self.multiline_buffer = []
                            break
                        except KeyboardInterrupt:
                            # Ctrl+C during continuation - cancel multiline
                            self.console.print(highlight=False)
                            self.multiline_buffer = []
                            break

                    # Join all lines for the complete command
                    if not self.multiline_buffer:
                        continue

                    # Join lines: preserve newlines in quotes, remove backslash continuations
                    full_command = []
                    for i, line in enumerate(self.multiline_buffer):
                        if line.rstrip().endswith('\\'):
                            # Backslash continuation: remove \ and don't add newline
                            full_command.append(line.rstrip()[:-1])
                        else:
                            # Regular line: add it
                            full_command.append(line)
                            # Add newline if not the last line
                            if i < len(self.multiline_buffer) - 1:
                                full_command.append('\n')

                    command = ''.join(full_command).strip()
                    self.multiline_buffer = []

                except EOFError:
                    # Ctrl+D - exit shell
                    self.console.print(highlight=False)
                    break
                except KeyboardInterrupt:
                    # Ctrl+C during input - just start new line
                    self.console.print(highlight=False)
                    self.multiline_buffer = []
                    continue

                # Handle special commands
                if command in ('exit', 'quit'):
                    break
                elif command == 'help':
                    self.show_help()
                    continue
                elif not command:
                    continue

                # Execute command
                try:
                    exit_code = self.execute(command)

                    # Check if for-loop is needed
                    if exit_code == EXIT_CODE_FOR_LOOP_NEEDED:
                        # Collect for/do/done loop
                        for_lines = [command]
                        for_depth = 1  # Track nesting depth
                        try:
                            while True:
                                for_line = input("> ")
                                for_lines.append(for_line)
                                # Count nested for loops
                                stripped = for_line.strip()
                                if stripped.startswith('for '):
                                    for_depth += 1
                                elif stripped == 'done':
                                    for_depth -= 1
                                    if for_depth == 0:
                                        break
                        except EOFError:
                            # Ctrl+D before done
                            self.console.print("\nWarning: for-loop ended by end-of-file (wanted `done`)", highlight=False)
                        except KeyboardInterrupt:
                            # Ctrl+C during for-loop - cancel
                            self.console.print("\n^C", highlight=False)
                            continue

                        # Execute the for loop
                        exit_code = self.execute_for_loop(for_lines)
                        # Update $? with the exit code
                        self.env['?'] = str(exit_code)

                    # Check if while-loop is needed
                    elif exit_code == EXIT_CODE_WHILE_LOOP_NEEDED:
                        # Collect while/do/done loop
                        while_lines = [command]
                        while_depth = 1  # Track nesting depth
                        try:
                            while True:
                                while_line = input("> ")
                                while_lines.append(while_line)
                                # Count nested while loops
                                stripped = while_line.strip()
                                if stripped.startswith('while '):
                                    while_depth += 1
                                elif stripped == 'done':
                                    while_depth -= 1
                                    if while_depth == 0:
                                        break
                        except EOFError:
                            # Ctrl+D before done
                            self.console.print("\nWarning: while-loop ended by end-of-file (wanted `done`)", highlight=False)
                        except KeyboardInterrupt:
                            # Ctrl+C during while-loop - cancel
                            self.console.print("\n^C", highlight=False)
                            continue

                        # Execute the while loop
                        exit_code = self.execute_while_loop(while_lines)
                        # Update $? with the exit code
                        self.env['?'] = str(exit_code)

                    # Check if if-statement is needed
                    elif exit_code == EXIT_CODE_IF_STATEMENT_NEEDED:
                        # Collect if/then/else/fi statement
                        if_lines = [command]
                        try:
                            while True:
                                if_line = input("> ")
                                if_lines.append(if_line)
                                # Check if we reached the end with 'fi'
                                if if_line.strip() == 'fi':
                                    break
                        except EOFError:
                            # Ctrl+D before fi
                            self.console.print("\nWarning: if-statement ended by end-of-file (wanted `fi`)", highlight=False)
                        except KeyboardInterrupt:
                            # Ctrl+C during if-statement - cancel
                            self.console.print("\n^C", highlight=False)
                            continue

                        # Execute the if statement
                        exit_code = self.execute_if_statement(if_lines)
                        # Update $? with the exit code
                        self.env['?'] = str(exit_code)

                    # Check if function definition is needed
                    elif exit_code == EXIT_CODE_FUNCTION_DEF_NEEDED:
                        # Collect function definition
                        func_lines = [command]
                        brace_depth = 1  # We've seen the opening {
                        try:
                            while True:
                                func_line = input("> ")
                                func_lines.append(func_line)
                                # Track braces
                                stripped = func_line.strip()
                                brace_depth += stripped.count('{')
                                brace_depth -= stripped.count('}')
                                if brace_depth == 0:
                                    break
                        except EOFError:
                            # Ctrl+D before closing }
                            self.console.print("\nWarning: function definition ended by end-of-file (wanted `}`)", highlight=False)
                        except KeyboardInterrupt:
                            # Ctrl+C during function definition - cancel
                            self.console.print("\n^C", highlight=False)
                            continue

                        # Parse and store the function using AST parser
                        func_ast = self.control_parser.parse_function_definition(func_lines)
                        if func_ast and func_ast.name:
                            # Store as AST-based function
                            self.functions[func_ast.name] = {
                                'name': func_ast.name,
                                'body': func_ast.body,
                                'is_ast': True
                            }
                            exit_code = 0
                        else:
                            self.console.print("[red]Syntax error: invalid function definition[/red]", highlight=False)
                            exit_code = 1

                        # Update $? with the exit code
                        self.env['?'] = str(exit_code)

                    # Check if heredoc is needed
                    elif exit_code == EXIT_CODE_HEREDOC_NEEDED:
                        # Parse command to get heredoc delimiter
                        commands, redirections = self.parser.parse_command_line(command)
                        if 'heredoc_delimiter' in redirections:
                            delimiter = redirections['heredoc_delimiter']

                            # Read heredoc content
                            heredoc_lines = []
                            try:
                                while True:
                                    heredoc_line = input()
                                    if heredoc_line.strip() == delimiter:
                                        break
                                    heredoc_lines.append(heredoc_line)
                            except EOFError:
                                # Ctrl+D before delimiter
                                self.console.print(f"\nWarning: here-document delimited by end-of-file (wanted `{delimiter}`)", highlight=False)
                            except KeyboardInterrupt:
                                # Ctrl+C during heredoc - cancel
                                self.console.print("\n^C", highlight=False)
                                continue

                            # Join heredoc content
                            heredoc_content = '\n'.join(heredoc_lines)
                            if heredoc_lines:  # Add final newline if there was content
                                heredoc_content += '\n'

                            # Execute command again with heredoc data
                            exit_code = self.execute(command, heredoc_data=heredoc_content.encode('utf-8'))
                            # Update $? with the exit code
                            self.env['?'] = str(exit_code)
                    else:
                        # Normal command execution - update $?
                        # Skip special exit codes for internal use
                        if exit_code not in [
                            EXIT_CODE_CONTINUE,
                            EXIT_CODE_BREAK,
                            EXIT_CODE_FOR_LOOP_NEEDED,
                            EXIT_CODE_WHILE_LOOP_NEEDED,
                            EXIT_CODE_IF_STATEMENT_NEEDED,
                            EXIT_CODE_HEREDOC_NEEDED,
                            EXIT_CODE_FUNCTION_DEF_NEEDED,
                            EXIT_CODE_RETURN
                        ]:
                            self.env['?'] = str(exit_code)

                except KeyboardInterrupt:
                    # Ctrl+C during command execution - interrupt command
                    self.console.print("\n^C", highlight=False)
                    continue
                except Exception as e:
                    self.console.print(f"[red]Error: {e}[/red]", highlight=False)

            except KeyboardInterrupt:
                # Ctrl+C at top level - start new line
                self.console.print(highlight=False)
                self.multiline_buffer = []
                continue

        # Save history before exiting
        # Use current value of HISTFILE variable (may have been changed during session)
        if 'HISTFILE' in self.env:
            try:
                import readline
                import os
                history_file = os.path.expanduser(self.env['HISTFILE'])
                readline.write_history_file(history_file)
            except Exception as e:
                self.console.print(f"[yellow]Warning: Could not save history: {e}[/yellow]", highlight=False)

        self.console.print("[cyan]Goodbye![/cyan]", highlight=False)

    def show_help(self):
        """Show help message"""
        help_text = """[bold cyan]agfs-shell[/bold cyan] - Experimental shell with AGFS integration

[bold yellow]File System Commands (AGFS):[/bold yellow]
  [green]cd[/green] [path]              - Change current directory (supports relative paths)
  [green]pwd[/green]                    - Print current working directory
  [green]ls[/green] [-l] [path]         - List directory contents (use -l for details, defaults to cwd)
  [green]mkdir[/green] path             - Create directory
  [green]rm[/green] [-r] path           - Remove file or directory
  [green]cat[/green] [file...]          - Read and concatenate files
  [green]stat[/green] path              - Display file status
  [green]cp[/green] [-r] src dest       - Copy files (local:path for local filesystem)
  [green]upload[/green] [-r] local agfs - Upload local file/directory to AGFS
  [green]download[/green] [-r] agfs local - Download AGFS file/directory to local

[bold yellow]Text Processing Commands:[/bold yellow]
  [green]echo[/green] [args...]         - Print arguments to stdout
  [green]grep[/green] [opts] pattern [files] - Search for pattern
    Options: -i (ignore case), -v (invert), -n (line numbers), -c (count)
  [green]jq[/green] filter [files]      - Process JSON data
  [green]wc[/green] [-l] [-w] [-c]      - Count lines, words, and bytes
  [green]head[/green] [-n count]        - Output first N lines (default 10)
  [green]tail[/green] [-n count]        - Output last N lines (default 10)
  [green]sort[/green] [-r]              - Sort lines (use -r for reverse)
  [green]uniq[/green]                   - Remove duplicate adjacent lines
  [green]tr[/green] set1 set2           - Translate characters

[bold yellow]Environment Variables:[/bold yellow]
  [green]export[/green] VAR=value       - Set environment variable
  [green]env[/green]                    - Display all environment variables
  [green]unset[/green] VAR              - Remove environment variable
  $VAR or ${{VAR}}          - Reference variable value

[bold yellow]Control Flow:[/bold yellow]
  [green]if[/green] condition; then
    commands
  elif condition; then
    commands
  else
    commands
  fi

  [green]for[/green] var in item1 item2 item3; do
    commands
  done

  [green]test[/green] or [green][[/green] expr [green]][/green]   - Test conditions
    File: -f (file), -d (directory), -e (exists)
    String: -z (empty), -n (non-empty), = (equal), != (not equal)
    Integer: -eq -ne -gt -lt -ge -le

[bold yellow]Pipeline Syntax:[/bold yellow]
  command1 | command2 | command3

[bold yellow]Multiline Input & Heredoc:[/bold yellow]
  Line ending with \\       - Continue on next line
  Unclosed quotes (" or ')  - Continue until closed
  Unclosed () or {{}}       - Continue until closed

  [green]cat << EOF[/green]           - Heredoc (write until EOF marker)
    Multiple lines of text
    Variables like $VAR are expanded
  EOF

  [green]cat << 'EOF'[/green]         - Literal heredoc (no expansion)
    Text with literal $VAR
  EOF

[bold yellow]Redirection Operators:[/bold yellow]
  < file                 - Read input from AGFS file
  > file                 - Write output to AGFS file (overwrite)
  >> file                - Append output to AGFS file
  2> file                - Write stderr to AGFS file
  2>> file               - Append stderr to AGFS file

[bold yellow]Path Resolution:[/bold yellow]
  - Absolute paths start with / (e.g., /local/file.txt)
  - Relative paths are resolved from current directory (e.g., file.txt, ../dir)
  - Special: . (current dir), .. (parent dir)
  - Tab completion works for both absolute and relative paths

[bold yellow]Examples:[/bold yellow]
  [dim]# File operations[/dim]
  [dim]>[/dim] cd /local/mydir
  [dim]>[/dim] cat file.txt | grep -i "error" | wc -l
  [dim]>[/dim] cp local:~/data.txt /local/backup.txt

  [dim]# Variables[/dim]
  [dim]>[/dim] export NAME="world"
  [dim]>[/dim] echo "Hello $NAME"

  [dim]# Conditionals[/dim]
  [dim]>[/dim] if test -f myfile.txt; then
         echo "File exists"
       else
         echo "File not found"
       fi

  [dim]# Loops[/dim]
  [dim]>[/dim] for file in *.txt; do
         echo "Processing $file"
         cat $file | grep "TODO"
       done

  [dim]# Heredoc[/dim]
  [dim]>[/dim] cat << EOF > config.json
       {
         "name": "$NAME",
         "version": "1.0"
       }
       EOF

  [dim]# JSON processing with jq[/dim]
  [dim]>[/dim] echo '{"name":"test","value":42}' | jq '.name'
  [dim]>[/dim] cat data.json | jq '.items[] | select(.active == true)'

  [dim]# Advanced grep[/dim]
  [dim]>[/dim] grep -n "function" code.py
  [dim]>[/dim] grep -r -i "error" *.log | grep -v "debug"

  [dim]# Sleep/delay execution[/dim]
  [dim]>[/dim] echo "Starting..." && sleep 2 && echo "Done!"
  [dim]>[/dim] for i in 1 2 3; do echo "Step $i"; sleep 1; done

[bold yellow]Utility Commands:[/bold yellow]
  [green]sleep[/green] seconds          - Pause execution for specified seconds (supports decimals)

[bold yellow]Special Commands:[/bold yellow]
  [green]help[/green]                   - Show this help
  [green]exit[/green], [green]quit[/green]             - Exit the shell
  [green]Ctrl+C[/green]                 - Interrupt current command
  [green]Ctrl+D[/green]                 - Exit the shell

[dim]Note: All file operations use AGFS. Paths like /local/, /s3fs/, /sqlfs/
      refer to different AGFS filesystem backends.[/dim]
"""
        self.console.print(help_text, highlight=False)
