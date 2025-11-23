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

    def _execute_command_substitution(self, command: str) -> str:
        """
        Execute a command and return its output as a string
        Used for command substitution: $(command) or `command`
        """
        from .streams import OutputStream, InputStream, ErrorStream
        from .builtins import get_builtin

        # Parse and execute the command, capturing stdout
        try:
            # Parse the command (no variable expansion to avoid recursion)
            commands, redirections = self.parser.parse_command_line(command)
            if not commands:
                return ''

            # Build processes for each command (simplified, no redirections)
            processes = []
            for i, (cmd, args) in enumerate(commands):
                executor = get_builtin(cmd)

                # Resolve paths for file commands (using metadata instead of hardcoded list)
                if CommandMetadata.needs_path_resolution(cmd):
                    resolved_args = []
                    for arg in args:
                        if arg.startswith('-'):
                            resolved_args.append(arg)
                        else:
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
                    env=self.env
                )
                process.cwd = self.cwd
                processes.append(process)

            # Connect pipeline
            for i in range(len(processes) - 1):
                output = processes[i].stdout
                processes[i + 1].stdin = InputStream.from_stream(output)

            # Execute pipeline
            for process in processes:
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

    def _expand_variables(self, text: str) -> str:
        """
        Expand environment variables and command substitutions in text
        Supports: $VAR, ${VAR}, $(command), `command`, and $? (exit code)
        """
        import re

        # First, expand special variables like $?
        # $? - exit code of last command
        text = text.replace('$?', self.env.get('?', '0'))

        # Then, expand command substitutions: $(command) and `command`
        # Process $(...) command substitution
        def replace_command_subst(match):
            command = match.group(1)
            return self._execute_command_substitution(command)

        text = re.sub(r'\$\(([^)]+)\)', replace_command_subst, text)

        # Process `...` command substitution (backticks)
        def replace_backtick_subst(match):
            command = match.group(1)
            return self._execute_command_substitution(command)

        text = re.sub(r'`([^`]+)`', replace_backtick_subst, text)

        # Then expand ${VAR} (higher priority than $VAR)
        def replace_braced(match):
            var_name = match.group(1)
            return self.env.get(var_name, '')

        text = re.sub(r'\$\{([A-Za-z_][A-Za-z0-9_]*)\}', replace_braced, text)

        # Finally expand $VAR
        def replace_simple(match):
            var_name = match.group(1)
            return self.env.get(var_name, '')

        text = re.sub(r'\$([A-Za-z_][A-Za-z0-9_]*)', replace_simple, text)

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
                # Check if argument contains glob characters
                if '*' in arg or '?' in arg or '[' in arg:
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
        parsed = self._parse_for_loop(lines)

        if not parsed:
            return 1

        var_name = parsed['var']
        items = parsed['items']
        commands = parsed['commands']

        # Execute loop for each item
        last_exit_code = 0
        for item in items:
            # Set loop variable
            self.env[var_name] = item

            # Execute commands in loop body
            i = 0
            while i < len(commands):
                cmd = commands[i]

                # Check if this is a nested for loop
                if cmd.strip().startswith('for '):
                    # Collect the nested for loop
                    nested_for_lines = [cmd]
                    for_depth = 1
                    i += 1
                    while i < len(commands):
                        next_cmd = commands[i]
                        nested_for_lines.append(next_cmd)
                        if next_cmd.strip().startswith('for '):
                            for_depth += 1
                        elif next_cmd.strip() == 'done':
                            for_depth -= 1
                            if for_depth == 0:
                                break
                        i += 1
                    # Execute the nested for loop
                    last_exit_code = self.execute_for_loop(nested_for_lines)
                # Check if this is a nested if statement
                elif cmd.strip().startswith('if '):
                    # Collect the nested if statement with depth tracking
                    nested_if_lines = [cmd]
                    if_depth = 1
                    i += 1
                    while i < len(commands):
                        next_cmd = commands[i]
                        nested_if_lines.append(next_cmd)
                        # Track nested if statements
                        if next_cmd.strip().startswith('if '):
                            if_depth += 1
                        elif next_cmd.strip() == 'fi':
                            if_depth -= 1
                            if if_depth == 0:
                                break
                        i += 1
                    # Execute the nested if statement
                    last_exit_code = self.execute_if_statement(nested_if_lines)
                else:
                    # Regular command
                    last_exit_code = self.execute(cmd)

                i += 1

        return last_exit_code

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

            if line == 'done':
                # End of for loop
                break
            elif line == 'do':
                state = 'do'
            elif line.startswith('do '):
                # 'do' with command on same line
                state = 'do'
                cmd_after_do = line[3:].strip()
                if cmd_after_do:
                    result['commands'].append(cmd_after_do)
            elif line.startswith('for '):
                # Only parse the FIRST for statement
                # Nested for loops should be treated as commands
                if not first_for_parsed:
                    # Parse: for var in item1 item2 item3
                    # or: for var in item1 item2 item3; do
                    parts = line[4:].strip()

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

                        # Expand variables in items string first
                        items_str = self._expand_variables(items_str)

                        # Split items by whitespace
                        # Use simple split() for word splitting after variable expansion
                        # This mimics bash's word splitting behavior
                        result['items'] = items_str.split()
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
                            if nested_line == 'done':
                                break
                            i += 1
            else:
                # Regular command in loop body
                if state == 'do':
                    result['commands'].append(line)

        return result if result['var'] else None

    def execute_if_statement(self, lines: List[str]) -> int:
        """
        Execute an if/then/else/fi statement

        Args:
            lines: List of lines making up the if statement

        Returns:
            Exit code of executed commands
        """
        parsed = self._parse_if_statement(lines)

        # Evaluate conditions in order
        for condition_cmd, commands_block in parsed['conditions']:
            # Execute the condition command
            exit_code = self.execute(condition_cmd)

            # If condition is true (exit code 0), execute this block
            if exit_code == 0:
                last_exit_code = 0
                for cmd in commands_block:
                    last_exit_code = self.execute(cmd)
                return last_exit_code

        # If no condition was true, execute else block if present
        if parsed['else_block']:
            last_exit_code = 0
            for cmd in parsed['else_block']:
                last_exit_code = self.execute(cmd)
            return last_exit_code

        return 0

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
                # Return special code -997 to signal for loop collection needed
                return -997

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
                # Return special code -998 to signal if statement collection needed
                return -998

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
                    self.env[var_name] = var_value
                    return 0

        # Expand variables in command line
        command_line = self._expand_variables(command_line)

        # Parse the command line with redirections
        commands, redirections = self.parser.parse_command_line(command_line)

        # Expand globs in command arguments
        commands = self._expand_globs(commands)

        # If heredoc is detected but no data provided, return special code to signal REPL
        # to read heredoc content
        if 'heredoc_delimiter' in redirections and heredoc_data is None:
            # Return special code -999 to signal that heredoc data is needed
            return -999

        # If heredoc data is provided, use it as stdin
        if heredoc_data is not None:
            stdin_data = heredoc_data

        if not commands:
            return 0

        # Special handling for cd command (must be a single command, not in pipeline)
        # Using metadata instead of hardcoded check
        if len(commands) == 1 and CommandMetadata.changes_cwd(commands[0][0]):
            cmd, args = commands[0]
            # Resolve target path
            target = args[0] if args else '/'
            resolved_path = self.resolve_path(target)

            # Verify the directory exists
            try:
                entries = self.filesystem.list_directory(resolved_path)
                # Successfully listed - it's a valid directory
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
                for arg in args:
                    # Skip flags (starting with -)
                    if arg.startswith('-'):
                        resolved_args.append(arg)
                    else:
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

            # Create process with filesystem, cwd, and env
            process = Process(
                command=cmd,
                args=args,
                stdin=stdin,
                stdout=stdout,
                stderr=stderr,
                executor=executor,
                filesystem=self.filesystem,
                env=self.env
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

                # Display write response if it contains data
                if write_response and write_response != "OK":
                    self.console.print(write_response, highlight=False)

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
                    write_response = self.filesystem.write_file(output_file, stdout_data, append=append)
                    # Display write response if it contains data
                    if write_response and write_response != "OK":
                        self.console.print(write_response, highlight=False)
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
                    if exit_code == -997:
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

                    # Check if if-statement is needed
                    elif exit_code == -998:
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

                    # Check if heredoc is needed
                    elif exit_code == -999:
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
                        if exit_code not in [-998, -999]:
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
