"""
Parser for shell control flow structures.

This module handles parsing of:
- for/while/until loops
- if/elif/else statements
- function definitions

The parser converts text lines into AST nodes defined in ast_nodes.py.
"""

from typing import List, Optional, Tuple
from .ast_nodes import (
    Statement, CommandStatement,
    ForStatement, WhileStatement, UntilStatement,
    IfStatement, IfBranch, FunctionDefinition
)
from .lexer import strip_comments
import re


class ParseError(Exception):
    """Raised when parsing fails"""
    def __init__(self, message: str, line_number: Optional[int] = None):
        self.line_number = line_number
        super().__init__(f"Parse error{f' at line {line_number}' if line_number else ''}: {message}")


class ControlParser:
    """
    Parser for shell control flow structures.

    This parser handles multi-line constructs and produces AST nodes.
    """

    def __init__(self, shell=None):
        """
        Initialize parser.

        Args:
            shell: Shell instance (optional, for access to _strip_comment method)
        """
        self.shell = shell

    def _strip_comment(self, line: str) -> str:
        """Strip comments from a line, respecting quotes"""
        return strip_comments(line)

    # ========================================================================
    # Main Parse Entry Points
    # ========================================================================

    def parse_for_loop(self, lines: List[str]) -> Optional[ForStatement]:
        """
        Parse a for loop from lines.

        Syntax:
            for VAR in ITEMS; do
                COMMANDS
            done

        Args:
            lines: Lines comprising the for loop

        Returns:
            ForStatement AST node or None on error
        """
        state = 'for'
        var_name = None
        items_raw = ""
        commands = []

        i = 0
        while i < len(lines):
            line = lines[i].strip()
            i += 1

            if not line or line.startswith('#'):
                continue

            line_no_comment = self._strip_comment(line).strip()

            if line_no_comment == 'done':
                break
            elif line_no_comment == 'do':
                state = 'do'
            elif line_no_comment.startswith('do '):
                state = 'do'
                cmd = line_no_comment[3:].strip()
                if cmd and cmd != 'done':
                    commands.append(cmd)
            elif line_no_comment.startswith('for ') and var_name is None:
                # Parse: for var in item1 item2 ...
                parts = line_no_comment[4:].strip()

                # Handle trailing '; do'
                if parts.endswith('; do'):
                    parts = parts[:-4].strip()
                    state = 'do'
                elif parts.endswith(' do'):
                    parts = parts[:-3].strip()
                    state = 'do'

                # Split by 'in'
                if ' in ' in parts:
                    var_part, items_part = parts.split(' in ', 1)
                    var_name = var_part.strip()
                    items_raw = self._strip_comment(items_part).strip()
                else:
                    return None  # Invalid syntax
            else:
                if state == 'do':
                    commands.append(line)

        if not var_name:
            return None

        # Parse commands into statements
        body = self._parse_block(commands)

        return ForStatement(
            variable=var_name,
            items_raw=items_raw,
            body=body
        )

    def parse_while_loop(self, lines: List[str]) -> Optional[WhileStatement]:
        """
        Parse a while loop from lines.

        Syntax:
            while CONDITION; do
                COMMANDS
            done
        """
        state = 'while'
        condition = None
        commands = []

        i = 0
        while i < len(lines):
            line = lines[i].strip()
            i += 1

            if not line or line.startswith('#'):
                continue

            line_no_comment = self._strip_comment(line).strip()

            if line_no_comment == 'done':
                break
            elif line_no_comment == 'do':
                state = 'do'
            elif line_no_comment.startswith('do '):
                state = 'do'
                cmd = line_no_comment[3:].strip()
                if cmd and cmd != 'done':
                    commands.append(cmd)
            elif line_no_comment.startswith('while ') and condition is None:
                cond = line_no_comment[6:].strip()

                if cond.endswith('; do'):
                    cond = cond[:-4].strip()
                    state = 'do'
                elif cond.endswith(' do'):
                    cond = cond[:-3].strip()
                    state = 'do'

                condition = self._strip_comment(cond)
            else:
                if state == 'do':
                    commands.append(line)

        if not condition:
            return None

        body = self._parse_block(commands)

        return WhileStatement(
            condition=condition,
            body=body
        )

    def parse_until_loop(self, lines: List[str]) -> Optional[UntilStatement]:
        """
        Parse an until loop from lines.

        Syntax:
            until CONDITION; do
                COMMANDS
            done
        """
        state = 'until'
        condition = None
        commands = []

        i = 0
        while i < len(lines):
            line = lines[i].strip()
            i += 1

            if not line or line.startswith('#'):
                continue

            line_no_comment = self._strip_comment(line).strip()

            if line_no_comment == 'done':
                break
            elif line_no_comment == 'do':
                state = 'do'
            elif line_no_comment.startswith('do '):
                state = 'do'
                cmd = line_no_comment[3:].strip()
                if cmd and cmd != 'done':
                    commands.append(cmd)
            elif line_no_comment.startswith('until ') and condition is None:
                cond = line_no_comment[6:].strip()

                if cond.endswith('; do'):
                    cond = cond[:-4].strip()
                    state = 'do'
                elif cond.endswith(' do'):
                    cond = cond[:-3].strip()
                    state = 'do'

                condition = self._strip_comment(cond)
            else:
                if state == 'do':
                    commands.append(line)

        if not condition:
            return None

        body = self._parse_block(commands)

        return UntilStatement(
            condition=condition,
            body=body
        )

    def parse_if_statement(self, lines: List[str]) -> Optional[IfStatement]:
        """
        Parse an if statement from lines.

        Syntax:
            if CONDITION; then
                COMMANDS
            [elif CONDITION; then
                COMMANDS]*
            [else
                COMMANDS]
            fi
        """
        branches = []
        current_condition = None
        current_commands = []
        state = 'start'  # start, condition, then, else

        for line in lines:
            line_stripped = line.strip()

            if not line_stripped or line_stripped.startswith('#'):
                continue

            line_no_comment = self._strip_comment(line_stripped).strip()

            if line_no_comment == 'fi':
                # Save last branch
                if state == 'then' and current_condition is not None:
                    branches.append(IfBranch(
                        condition=current_condition,
                        body=self._parse_block(current_commands)
                    ))
                elif state == 'else':
                    # else_commands already in current_commands
                    pass
                break

            elif line_no_comment == 'then':
                state = 'then'
                current_commands = []

            elif line_no_comment.startswith('then '):
                state = 'then'
                current_commands = []
                cmd = line_no_comment[5:].strip()
                if cmd and cmd != 'fi':
                    current_commands.append(cmd)

            elif line_no_comment.startswith('elif '):
                # Save previous branch
                if current_condition is not None:
                    branches.append(IfBranch(
                        condition=current_condition,
                        body=self._parse_block(current_commands)
                    ))

                # Parse elif condition
                cond = line_no_comment[5:].strip()
                cond = self._strip_comment(cond)
                if cond.endswith('; then'):
                    cond = cond[:-6].strip()
                    state = 'then'
                    current_commands = []
                elif cond.endswith(' then'):
                    cond = cond[:-5].strip()
                    state = 'then'
                    current_commands = []
                else:
                    state = 'condition'
                current_condition = cond.rstrip(';')

            elif line_no_comment == 'else':
                # Save previous branch
                if current_condition is not None:
                    branches.append(IfBranch(
                        condition=current_condition,
                        body=self._parse_block(current_commands)
                    ))
                state = 'else'
                current_condition = None
                current_commands = []

            elif line_no_comment.startswith('else '):
                # Save previous branch
                if current_condition is not None:
                    branches.append(IfBranch(
                        condition=current_condition,
                        body=self._parse_block(current_commands)
                    ))
                state = 'else'
                current_condition = None
                current_commands = []
                cmd = line_no_comment[5:].strip()
                if cmd and cmd != 'fi':
                    current_commands.append(cmd)

            elif line_no_comment.startswith('if ') and state == 'start':
                cond = line_no_comment[3:].strip()
                cond = self._strip_comment(cond)
                if cond.endswith('; then'):
                    cond = cond[:-6].strip()
                    state = 'then'
                    current_commands = []
                elif cond.endswith(' then'):
                    cond = cond[:-5].strip()
                    state = 'then'
                    current_commands = []
                else:
                    state = 'condition'
                current_condition = cond.rstrip(';')

            else:
                if state in ('then', 'else'):
                    current_commands.append(line_stripped)

        if not branches and current_condition is None:
            return None

        # Handle else block
        else_body = None
        if state == 'else' and current_commands:
            else_body = self._parse_block(current_commands)

        return IfStatement(
            branches=branches,
            else_body=else_body
        )

    @staticmethod
    def _parse_function_params(params_str: Optional[str]) -> List[str]:
        """Parse a comma-separated parameter list from a function header.

        ``None`` / empty / whitespace-only -> ``[]``. Whitespace around each
        name is stripped. Declared names are stored for introspection only;
        call-time binding still uses ``$1``, ``$2``, ... .
        """
        if not params_str or not params_str.strip():
            return []
        return [p.strip() for p in params_str.split(',') if p.strip()]

    # Regex fragments for the supported function-definition headers:
    #   name() { ... }
    #   name(a, b) { ... }
    #   function name { ... }
    #   function name() { ... }
    #   function name(a, b) { ... }
    # The bash form makes the parameter list optional; the POSIX form
    # requires the parens (with or without typed params).
    _FUNCTION_HEADER_POSIX = (
        r'^([A-Za-z_][A-Za-z0-9_]*)\s*\(([^)]*)\)'
    )
    _FUNCTION_HEADER_BASH = (
        r'^function\s+([A-Za-z_][A-Za-z0-9_]*)\s*(?:\(([^)]*)\))?'
    )

    def parse_function_definition(self, lines: List[str]) -> Optional[FunctionDefinition]:
        """Parse a function definition from lines"""
        if not lines:
            return None

        first_line = lines[0].strip()

        # Try single-line function: name() { cmd; } / function name() { cmd; }
        match = re.match(self._FUNCTION_HEADER_POSIX + r'\s*\{(.+)\}$', first_line)
        if not match:
            match = re.match(self._FUNCTION_HEADER_BASH + r'\s*\{(.+)\}$', first_line)

        if match:
            name = match.group(1)
            params = self._parse_function_params(match.group(2))
            body_str = match.group(3).strip()
            commands = [cmd.strip() for cmd in body_str.split(';') if cmd.strip()]
            return FunctionDefinition(
                name=name,
                params=params,
                body=self._parse_block(commands)
            )

        # Multi-line function header: name() / function name [()] [{]
        match = re.match(self._FUNCTION_HEADER_POSIX + r'\s*\{?\s*$', first_line)
        if not match:
            match = re.match(self._FUNCTION_HEADER_BASH + r'\s*\{?\s*$', first_line)

        if not match:
            return None

        name = match.group(1)
        params = self._parse_function_params(match.group(2))
        commands = []

        # Collect body
        start = 1
        if not first_line.rstrip().endswith('{') and start < len(lines) and lines[start].strip() == '{':
            start += 1

        brace_depth = 1
        for i in range(start, len(lines)):
            line = lines[i].strip()
            if not line or line.startswith('#'):
                continue
            if line == '}':
                brace_depth -= 1
                if brace_depth == 0:
                    break
            elif '{' in line:
                brace_depth += line.count('{') - line.count('}')
            commands.append(lines[i])

        return FunctionDefinition(
            name=name,
            params=params,
            body=self._parse_block(commands)
        )

    # ========================================================================
    # Block Parsing - Unified nested structure handling
    # ========================================================================

    def _parse_block(self, commands: List[str]) -> List[Statement]:
        """
        Parse a list of command strings into a list of Statements.

        This handles nested structures by detecting keywords and
        collecting the appropriate lines.
        """
        statements = []
        i = 0

        while i < len(commands):
            cmd = commands[i].strip()
            cmd_no_comment = self._strip_comment(cmd).strip()

            if not cmd or cmd.startswith('#'):
                i += 1
                continue

            # Check for nested for loop
            if cmd_no_comment.startswith('for '):
                nested_lines, end_idx = self._collect_block(commands, i, 'for', 'done')
                stmt = self.parse_for_loop(nested_lines)
                if stmt:
                    statements.append(stmt)
                i = end_idx + 1

            # Check for nested while loop
            elif cmd_no_comment.startswith('while '):
                nested_lines, end_idx = self._collect_block(commands, i, 'while', 'done')
                stmt = self.parse_while_loop(nested_lines)
                if stmt:
                    statements.append(stmt)
                i = end_idx + 1

            # Check for nested until loop
            elif cmd_no_comment.startswith('until '):
                nested_lines, end_idx = self._collect_block(commands, i, 'until', 'done')
                stmt = self.parse_until_loop(nested_lines)
                if stmt:
                    statements.append(stmt)
                i = end_idx + 1

            # Check for nested if statement
            elif cmd_no_comment.startswith('if '):
                nested_lines, end_idx = self._collect_block_if(commands, i)
                stmt = self.parse_if_statement(nested_lines)
                if stmt:
                    statements.append(stmt)
                i = end_idx + 1

            # Regular command
            else:
                statements.append(CommandStatement(command=cmd))
                i += 1

        return statements

    def _collect_block(self, commands: List[str], start: int,
                       start_keyword: str, end_keyword: str) -> Tuple[List[str], int]:
        """
        Collect lines for a block structure (for/while/until ... done).

        Returns (collected_lines, end_index)
        """
        lines = [commands[start]]
        depth = 1
        i = start + 1

        while i < len(commands):
            line = commands[i]
            line_no_comment = self._strip_comment(line).strip()
            lines.append(line)

            if line_no_comment.startswith(f'{start_keyword} '):
                depth += 1
            elif line_no_comment == end_keyword:
                depth -= 1
                if depth == 0:
                    break
            i += 1

        return lines, i

    def _collect_block_if(self, commands: List[str], start: int) -> Tuple[List[str], int]:
        """
        Collect lines for an if statement (if ... fi).

        Returns (collected_lines, end_index)
        """
        lines = [commands[start]]
        depth = 1
        i = start + 1

        while i < len(commands):
            line = commands[i]
            line_no_comment = self._strip_comment(line).strip()
            lines.append(line)

            if line_no_comment.startswith('if '):
                depth += 1
            elif line_no_comment == 'fi':
                depth -= 1
                if depth == 0:
                    break
            i += 1

        return lines, i
