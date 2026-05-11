"""
Expression evaluation framework for shell

This module provides a unified framework for evaluating shell expressions:
- Variable expansion: $VAR, ${VAR}, ${VAR:-default}, etc.
- Arithmetic evaluation: $((expr))
- Command substitution: $(cmd), `cmd`
- Escape sequences: $'...' syntax and backslash escapes

Design principles:
- Single source of truth for expansion logic
- Reusable components (BracketMatcher, QuoteTracker)
- Support for Bash-style parameter expansion modifiers
- Safe arithmetic evaluation without eval()
"""

import re
import ast
import operator
from typing import Optional, Callable, Tuple, List, TYPE_CHECKING
from dataclasses import dataclass
from .lexer import QuoteTracker

if TYPE_CHECKING:
    from .shell import Shell


# =============================================================================
# Utility Classes
# =============================================================================

class EscapeHandler:
    """
    Handles escape sequences in shell strings

    Supports:
    - $'...' ANSI-C quoting syntax (full escape support)
    - Backslash escapes in double quotes (limited: \\\\, \\$, \\`, \\", \\newline)

    Escape sequences supported in $'...':
    - \\n  newline
    - \\t  tab
    - \\r  carriage return
    - \\a  alert (bell)
    - \\b  backspace
    - \\e  escape character
    - \\f  form feed
    - \\v  vertical tab
    - \\\\  backslash
    - \\'  single quote
    - \\"  double quote
    - \\xHH  hex byte
    - \\nnn  octal byte
    """

    # Escape sequences for $'...' syntax
    ESCAPE_MAP = {
        'n': '\n',
        't': '\t',
        'r': '\r',
        'a': '\a',
        'b': '\b',
        'e': '\x1b',  # escape character
        'f': '\f',
        'v': '\v',
        '\\': '\\',
        "'": "'",
        '"': '"',
        '0': '\0',
    }

    @classmethod
    def process_escapes(cls, text: str) -> str:
        """
        Process escape sequences in text

        Args:
            text: Text that may contain escape sequences

        Returns:
            Text with escape sequences expanded
        """
        result = []
        i = 0

        while i < len(text):
            if text[i] == '\\' and i + 1 < len(text):
                next_char = text[i + 1]

                # Check simple escapes
                if next_char in cls.ESCAPE_MAP:
                    result.append(cls.ESCAPE_MAP[next_char])
                    i += 2
                    continue

                # Hex escape: \xHH
                if next_char == 'x' and i + 3 < len(text):
                    hex_digits = text[i+2:i+4]
                    if all(c in '0123456789abcdefABCDEF' for c in hex_digits):
                        result.append(chr(int(hex_digits, 16)))
                        i += 4
                        continue

                # Octal escape: \nnn (1-3 digits)
                if next_char in '0123456789':
                    octal = ''
                    j = i + 1
                    while j < len(text) and j < i + 4 and text[j] in '01234567':
                        octal += text[j]
                        j += 1
                    if octal:
                        value = int(octal, 8)
                        if value <= 255:
                            result.append(chr(value))
                            i = j
                            continue

                # Unknown escape - keep as is
                result.append(text[i])
                i += 1
            else:
                result.append(text[i])
                i += 1

        return ''.join(result)

    @classmethod
    def expand_dollar_single_quotes(cls, text: str) -> str:
        """
        Expand $'...' ANSI-C quoting syntax

        Args:
            text: Text that may contain $'...' sequences

        Returns:
            Text with $'...' expanded (quotes removed, escapes processed)
        """
        result = []
        i = 0

        while i < len(text):
            # Look for $'
            if text[i:i+2] == "$'":
                # Find matching closing quote
                start = i
                i += 2
                content = []

                while i < len(text):
                    if text[i] == '\\' and i + 1 < len(text):
                        # Escape sequence - include both chars for later processing
                        content.append(text[i:i+2])
                        i += 2
                    elif text[i] == "'":
                        # End of $'...'
                        escaped_content = cls.process_escapes(''.join(content))
                        result.append(escaped_content)
                        i += 1
                        break
                    else:
                        content.append(text[i])
                        i += 1
                else:
                    # Unclosed $' - keep original
                    result.append(text[start:])
            else:
                result.append(text[i])
                i += 1

        return ''.join(result)

    # Limited escapes allowed in double quotes (Bash behavior)
    DOUBLE_QUOTE_ESCAPES = {'\\', '$', '"', '`', '\n'}

    # Placeholder for escaped characters (to prevent re-expansion)
    # Using private use area characters that won't appear in normal text
    ESCAPED_DOLLAR = '\ue000'
    ESCAPED_BACKTICK = '\ue001'
    ESCAPED_BACKSLASH = '\ue002'

    @classmethod
    def process_double_quote_escapes(cls, text: str) -> str:
        """
        Process escape sequences inside double-quoted strings

        In Bash, only these escapes are special inside double quotes:
        - \\\\  literal backslash
        - \\$   literal dollar sign
        - \\"   literal double quote
        - \\`   literal backtick
        - \\newline  line continuation (removed)

        Other \\X sequences are kept as-is (backslash is preserved).

        Args:
            text: Content inside double quotes (without the quotes)

        Returns:
            Text with escapes processed
        """
        result = []
        i = 0

        while i < len(text):
            if text[i] == '\\' and i + 1 < len(text):
                next_char = text[i + 1]
                if next_char in cls.DOUBLE_QUOTE_ESCAPES:
                    if next_char == '\n':
                        # Line continuation - skip both backslash and newline
                        i += 2
                        continue
                    else:
                        # Valid escape - output just the character
                        result.append(next_char)
                        i += 2
                        continue
                # Not a valid escape in double quotes - keep backslash
                result.append(text[i])
                i += 1
            else:
                result.append(text[i])
                i += 1

        return ''.join(result)

    @classmethod
    def expand_double_quote_escapes(cls, text: str) -> str:
        """
        Process escapes inside double-quoted portions of text

        Finds "..." sections and processes escapes within them.
        Uses placeholders for escaped $, `, \\ to prevent re-expansion.

        Args:
            text: Full text that may contain double-quoted strings

        Returns:
            Text with double-quote escapes processed (placeholders used)
        """
        result = []
        i = 0
        in_single_quote = False

        while i < len(text):
            char = text[i]

            # Track single quotes (no escape processing inside)
            if char == "'" and not in_single_quote:
                # Check if this is $'...' which is handled separately
                if i > 0 and text[i-1] == '$':
                    result.append(char)
                    i += 1
                    continue
                in_single_quote = True
                result.append(char)
                i += 1
                continue
            elif char == "'" and in_single_quote:
                in_single_quote = False
                result.append(char)
                i += 1
                continue

            if in_single_quote:
                result.append(char)
                i += 1
                continue

            # Handle double quotes
            if char == '"':
                result.append(char)  # Keep opening quote
                i += 1
                content = []

                # Collect content until closing quote
                while i < len(text):
                    if text[i] == '\\' and i + 1 < len(text):
                        next_char = text[i + 1]
                        if next_char in cls.DOUBLE_QUOTE_ESCAPES:
                            if next_char == '\n':
                                # Line continuation - skip both
                                i += 2
                                continue
                            elif next_char == '$':
                                # Use placeholder to prevent variable expansion
                                content.append(cls.ESCAPED_DOLLAR)
                                i += 2
                                continue
                            elif next_char == '`':
                                # Use placeholder to prevent command substitution
                                content.append(cls.ESCAPED_BACKTICK)
                                i += 2
                                continue
                            elif next_char == '\\':
                                # Use placeholder
                                content.append(cls.ESCAPED_BACKSLASH)
                                i += 2
                                continue
                            else:
                                # Valid escape (like \")
                                content.append(next_char)
                                i += 2
                                continue
                        # Not valid - keep backslash and char
                        content.append(text[i])
                        i += 1
                    elif text[i] == '"':
                        # End of double quote
                        result.append(''.join(content))
                        result.append('"')  # Keep closing quote
                        i += 1
                        break
                    else:
                        content.append(text[i])
                        i += 1
                else:
                    # Unclosed quote - append what we have
                    result.append(''.join(content))
            else:
                result.append(char)
                i += 1

        return ''.join(result)

    @classmethod
    def restore_escaped_chars(cls, text: str) -> str:
        """
        Restore placeholder characters to their original values

        Called after all expansions are complete.
        """
        return (text
                .replace(cls.ESCAPED_DOLLAR, '$')
                .replace(cls.ESCAPED_BACKTICK, '`')
                .replace(cls.ESCAPED_BACKSLASH, '\\'))


class BracketMatcher:
    """
    Utility class for finding matching brackets/parentheses in text

    Handles:
    - Nested brackets
    - Quote-awareness (brackets inside quotes don't count)
    - Multiple bracket types: (), {}, []
    """

    BRACKETS = {
        '(': ')',
        '{': '}',
        '[': ']',
    }

    @classmethod
    def find_matching_close(cls, text: str, open_pos: int) -> int:
        """
        Find the position of the matching closing bracket

        Args:
            text: Text to search in
            open_pos: Position of the opening bracket

        Returns:
            Position of matching closing bracket, or -1 if not found
        """
        if open_pos >= len(text):
            return -1

        open_char = text[open_pos]
        if open_char not in cls.BRACKETS:
            return -1

        close_char = cls.BRACKETS[open_char]
        depth = 1
        tracker = QuoteTracker()

        i = open_pos + 1
        while i < len(text):
            char = text[i]
            tracker.process_char(char)

            if not tracker.is_quoted():
                if char == open_char:
                    depth += 1
                elif char == close_char:
                    depth -= 1
                    if depth == 0:
                        return i
            i += 1

        return -1

    @classmethod
    def extract_balanced(cls, text: str, start: int,
                         open_char: str, close_char: str) -> Tuple[str, int]:
        """
        Extract content between balanced brackets

        Args:
            text: Text to extract from
            start: Position of opening bracket
            open_char: Opening bracket character
            close_char: Closing bracket character

        Returns:
            Tuple of (content between brackets, position after closing bracket)
            Returns ('', start) if not found
        """
        if start >= len(text) or text[start] != open_char:
            return '', start

        depth = 1
        tracker = QuoteTracker()
        content = []
        i = start + 1

        while i < len(text):
            char = text[i]
            tracker.process_char(char)

            if not tracker.is_quoted():
                if char == open_char:
                    depth += 1
                elif char == close_char:
                    depth -= 1
                    if depth == 0:
                        return ''.join(content), i + 1

            content.append(char)
            i += 1

        # Unbalanced - return what we have
        return ''.join(content), i


# =============================================================================
# Parameter Expansion
# =============================================================================

@dataclass
class ParameterExpansion:
    """
    Represents a parameter expansion like ${VAR:-default}

    Attributes:
        var_name: Variable name
        modifier: Modifier character (-, +, =, ?, #, %, /)
        modifier_arg: Argument to modifier (e.g., default value)
        greedy: Whether modifier is greedy (## vs #, %% vs %)
    """
    var_name: str
    modifier: Optional[str] = None
    modifier_arg: Optional[str] = None
    greedy: bool = False


class ParameterExpander:
    """
    Handles Bash-style parameter expansion

    Supports:
    - ${VAR}           - Simple expansion
    - ${VAR:-default}  - Use default if unset or null
    - ${VAR:=default}  - Assign default if unset or null
    - ${VAR:+value}    - Use value if set and non-null
    - ${VAR:?error}    - Error if unset or null
    - ${VAR#pattern}   - Remove shortest prefix matching pattern
    - ${VAR##pattern}  - Remove longest prefix matching pattern
    - ${VAR%pattern}   - Remove shortest suffix matching pattern
    - ${VAR%%pattern}  - Remove longest suffix matching pattern
    - ${#VAR}          - String length
    """

    # Pattern for parsing ${...} content
    # Matches: VAR, VAR:-default, VAR#pattern, #VAR, etc.
    MODIFIER_PATTERN = re.compile(
        r'^(?P<length>#)?'                      # Optional # for length
        r'(?P<name>[A-Za-z_][A-Za-z0-9_]*|\d+)' # Variable name or positional
        r'(?::?(?P<mod>[-+=?#%])(?P<greedy>[#%])?(?P<arg>.*))?$'  # Optional modifier
    )

    def __init__(self, get_variable: Callable[[str], str],
                 set_variable: Optional[Callable[[str, str], None]] = None):
        """
        Initialize expander

        Args:
            get_variable: Function to get variable value
            set_variable: Function to set variable value (for := modifier)
        """
        self.get_variable = get_variable
        self.set_variable = set_variable

    def parse(self, content: str) -> Optional[ParameterExpansion]:
        """
        Parse parameter expansion content (without ${})

        Args:
            content: Content inside ${}

        Returns:
            ParameterExpansion object or None if invalid
        """
        # Handle ${#VAR} (length)
        if content.startswith('#') and len(content) > 1:
            var_name = content[1:]
            if re.match(r'^[A-Za-z_][A-Za-z0-9_]*$|^\d+$', var_name):
                return ParameterExpansion(var_name=var_name, modifier='length')

        # Try to match modifier patterns
        match = self.MODIFIER_PATTERN.match(content)
        if not match:
            # Simple variable name?
            if re.match(r'^[A-Za-z_][A-Za-z0-9_]*$|^\d+$', content):
                return ParameterExpansion(var_name=content)
            return None

        var_name = match.group('name')
        modifier = match.group('mod')
        greedy = bool(match.group('greedy'))
        arg = match.group('arg') or ''

        # Check for length prefix
        if match.group('length'):
            return ParameterExpansion(var_name=var_name, modifier='length')

        return ParameterExpansion(
            var_name=var_name,
            modifier=modifier,
            modifier_arg=arg,
            greedy=greedy
        )

    def expand(self, expansion: ParameterExpansion) -> str:
        """
        Evaluate a parameter expansion

        Args:
            expansion: Parsed expansion

        Returns:
            Expanded value
        """
        value = self.get_variable(expansion.var_name)

        if expansion.modifier is None:
            return value

        if expansion.modifier == 'length':
            return str(len(value))

        if expansion.modifier == '-':
            # ${VAR:-default} - use default if empty
            return value if value else expansion.modifier_arg

        if expansion.modifier == '+':
            # ${VAR:+value} - use value if set
            return expansion.modifier_arg if value else ''

        if expansion.modifier == '=':
            # ${VAR:=default} - assign default if empty
            if not value:
                value = expansion.modifier_arg
                if self.set_variable:
                    self.set_variable(expansion.var_name, value)
            return value

        if expansion.modifier == '?':
            # ${VAR:?error} - error if empty
            if not value:
                # In a real shell, this would print error and exit
                # For now, just return empty
                return ''
            return value

        if expansion.modifier == '#':
            # ${VAR#pattern} or ${VAR##pattern} - remove prefix
            pattern = expansion.modifier_arg
            if expansion.greedy:
                # Remove longest matching prefix
                return self._remove_prefix_greedy(value, pattern)
            else:
                # Remove shortest matching prefix
                return self._remove_prefix(value, pattern)

        if expansion.modifier == '%':
            # ${VAR%pattern} or ${VAR%%pattern} - remove suffix
            pattern = expansion.modifier_arg
            if expansion.greedy:
                return self._remove_suffix_greedy(value, pattern)
            else:
                return self._remove_suffix(value, pattern)

        return value

    def _glob_to_regex(self, pattern: str) -> str:
        """Convert shell glob pattern to regex"""
        result = []
        i = 0
        while i < len(pattern):
            c = pattern[i]
            if c == '*':
                result.append('.*')
            elif c == '?':
                result.append('.')
            elif c in '.^$+{}[]|()\\':
                result.append('\\' + c)
            else:
                result.append(c)
            i += 1
        return ''.join(result)

    def _remove_prefix(self, value: str, pattern: str) -> str:
        """Remove shortest matching prefix"""
        regex = '^' + self._glob_to_regex(pattern)
        match = re.match(regex, value)
        if match:
            # Find shortest match
            for i in range(1, len(value) + 1):
                if re.match(regex + '$', value[:i]):
                    return value[i:]
            return value[match.end():]
        return value

    def _remove_prefix_greedy(self, value: str, pattern: str) -> str:
        """Remove longest matching prefix"""
        regex = '^' + self._glob_to_regex(pattern)
        match = re.match(regex, value)
        if match:
            return value[match.end():]
        return value

    def _remove_suffix(self, value: str, pattern: str) -> str:
        """Remove shortest matching suffix"""
        regex = self._glob_to_regex(pattern) + '$'
        match = re.search(regex, value)
        if match:
            # Find shortest match by trying from end
            for i in range(len(value) - 1, -1, -1):
                if re.match('^' + self._glob_to_regex(pattern) + '$', value[i:]):
                    return value[:i]
            return value[:match.start()]
        return value

    def _remove_suffix_greedy(self, value: str, pattern: str) -> str:
        """Remove longest matching suffix"""
        regex = self._glob_to_regex(pattern) + '$'
        match = re.search(regex, value)
        if match:
            return value[:match.start()]
        return value


# =============================================================================
# Arithmetic Evaluation
# =============================================================================

class ArithmeticEvaluator:
    """
    Safe arithmetic expression evaluator

    Uses Python's AST to safely evaluate arithmetic expressions
    without using dangerous eval().

    Supports:
    - Basic operators: +, -, *, /, %, **
    - Unary operators: +, -
    - Parentheses
    - Integer and float literals
    - Variable references (via callback)
    """

    ALLOWED_OPS = {
        ast.Add: operator.add,
        ast.Sub: operator.sub,
        ast.Mult: operator.mul,
        ast.Div: operator.truediv,
        ast.FloorDiv: operator.floordiv,
        ast.Mod: operator.mod,
        ast.Pow: operator.pow,
        ast.USub: operator.neg,
        ast.UAdd: operator.pos,
    }

    def __init__(self, get_variable: Callable[[str], str]):
        """
        Initialize evaluator

        Args:
            get_variable: Function to get variable value
        """
        self.get_variable = get_variable

    def evaluate(self, expr: str) -> int:
        """
        Evaluate an arithmetic expression

        Args:
            expr: Expression string (e.g., "5 + 3 * 2")

        Returns:
            Integer result (Bash arithmetic uses integers)
        """
        try:
            # Expand variables in expression
            expanded = self._expand_variables(expr)

            # Parse and evaluate
            tree = ast.parse(expanded.strip(), mode='eval')
            result = self._eval_node(tree.body)

            return int(result)
        except Exception:
            # Any error returns 0 (Bash behavior)
            return 0

    def _expand_variables(self, expr: str) -> str:
        """Expand variables in arithmetic expression"""
        result = expr

        # Expand ${VAR} format
        for match in re.finditer(r'\$\{([A-Za-z_][A-Za-z0-9_]*|\d+)\}', expr):
            var_name = match.group(1)
            value = self._get_numeric_value(var_name)
            result = result.replace(f'${{{var_name}}}', value)

        # Expand $VAR and $N format
        for match in re.finditer(r'\$([A-Za-z_][A-Za-z0-9_]*|\d+)', result):
            var_name = match.group(1)
            value = self._get_numeric_value(var_name)
            result = result.replace(f'${var_name}', value)

        # Expand bare variable names (VAR without $)
        # Be careful not to replace keywords
        keywords = {'and', 'or', 'not', 'in', 'is', 'if', 'else'}
        for match in re.finditer(r'\b([A-Za-z_][A-Za-z0-9_]*)\b', result):
            var_name = match.group(1)
            if var_name in keywords:
                continue
            value = self.get_variable(var_name)
            if value:
                try:
                    int(value)
                    result = result.replace(var_name, value)
                except ValueError:
                    pass

        return result

    def _get_numeric_value(self, var_name: str) -> str:
        """Get variable value as numeric string"""
        value = self.get_variable(var_name) or '0'
        try:
            int(value)
            return value
        except ValueError:
            return '0'

    def _eval_node(self, node) -> float:
        """Recursively evaluate AST node.

        ``ast.Constant`` covers every numeric literal in Python 3.8+
        (the project's minimum). The previous code also had a
        ``hasattr(ast, 'Num') and isinstance(node, ast.Num)`` branch
        for "Python 3.7 compatibility", but:

        * Python 3.7 is end-of-life and below the project's pyproject
          ``requires-python = ">=3.8"``.
        * In 3.8-3.13 ``ast.Num`` is a deprecated shim that emits a
          DeprecationWarning on every attribute lookup — visible in
          our test suite as 9 warnings per arithmetic test.
        * Python 3.14 removes ``ast.Num`` entirely; even the
          ``hasattr`` guard couldn't make that branch live again.

        Dropping the deprecated branch silences the warning today and
        makes the code Python-3.14-safe. Any future literal type
        (e.g. ``ast.JoinedStr``) would still hit the
        ``Node type … not allowed`` error path below — that's the
        intended safety net.
        """
        if isinstance(node, ast.Constant):
            if isinstance(node.value, (int, float)):
                return node.value
            raise ValueError(f"Only numeric constants allowed, got {type(node.value)}")

        if isinstance(node, ast.BinOp):
            if type(node.op) not in self.ALLOWED_OPS:
                raise ValueError(f"Operator {type(node.op).__name__} not allowed")
            left = self._eval_node(node.left)
            right = self._eval_node(node.right)
            return self.ALLOWED_OPS[type(node.op)](left, right)

        if isinstance(node, ast.UnaryOp):
            if type(node.op) not in self.ALLOWED_OPS:
                raise ValueError(f"Operator {type(node.op).__name__} not allowed")
            operand = self._eval_node(node.operand)
            return self.ALLOWED_OPS[type(node.op)](operand)

        raise ValueError(f"Node type {type(node).__name__} not allowed")


# =============================================================================
# Main Expression Expander
# =============================================================================

class ExpressionExpander:
    """
    Main class for expanding all types of shell expressions

    This is the unified entry point for expression expansion.
    It handles the correct order of expansions:
    1. Command substitution $(cmd) and `cmd`
    2. Arithmetic expansion $((expr))
    3. Parameter expansion ${VAR}, $VAR

    Usage:
        expander = ExpressionExpander(shell)
        result = expander.expand("Hello ${USER:-world}! Sum: $((1+2))")
    """

    def __init__(self, shell: 'Shell'):
        """
        Initialize expander with shell context

        Args:
            shell: Shell instance for variable access and command execution
        """
        self.shell = shell
        self.param_expander = ParameterExpander(
            get_variable=shell._get_variable,
            set_variable=lambda n, v: shell._set_variable(n, v)
        )
        self.arith_evaluator = ArithmeticEvaluator(
            get_variable=shell._get_variable
        )

    # Placeholder for single-quoted content (Unicode private use area)
    SINGLE_QUOTE_PLACEHOLDER_PREFIX = '\ue100'

    def expand(self, text: str) -> str:
        """
        Expand all expressions in text

        Expansion order:
        0. Protect single-quoted content (no expansion inside single quotes)
        1. $'...' ANSI-C quoting (escape sequences)
        2. Command substitution $(cmd) and `cmd`
        3. Arithmetic $((expr))
        4. Parameter expansion ${VAR}, $VAR
        5. Restore single-quoted content

        Args:
            text: Text containing expressions

        Returns:
            Fully expanded text
        """
        # Step 0: Protect single-quoted content from expansion
        # In Bash, content inside '...' is literal (no expansion)
        text, single_quoted_parts = self._protect_single_quoted(text)

        # Step 1: $'...' ANSI-C quoting with escape sequences
        text = EscapeHandler.expand_dollar_single_quotes(text)

        # Step 2: Command substitution
        text = self._expand_command_substitution(text)

        # Step 3: Arithmetic expansion
        text = self._expand_arithmetic(text)

        # Step 4: Parameter expansion
        text = self._expand_parameters(text)

        # Step 5: Restore single-quoted content (without the quotes)
        text = self._restore_single_quoted(text, single_quoted_parts)

        return text

    def _protect_single_quoted(self, text: str) -> Tuple[str, List[str]]:
        """
        Replace single-quoted sections with placeholders.

        Returns:
            Tuple of (text with placeholders, list of original contents)
        """
        result = []
        single_quoted_parts = []
        i = 0
        in_double_quote = False

        while i < len(text):
            char = text[i]

            # Track double quotes (single quotes inside double quotes are literal)
            if char == '"' and (i == 0 or text[i-1] != '\\'):
                in_double_quote = not in_double_quote
                result.append(char)
                i += 1
                continue

            # Check for single quote outside of double quotes
            # Also skip $'...' which is ANSI-C quoting (handled separately)
            if char == "'" and not in_double_quote:
                # Check if this is $'...' (ANSI-C quoting)
                if i > 0 and text[i-1] == '$':
                    result.append(char)
                    i += 1
                    continue

                # Found start of single-quoted string
                i += 1  # Skip opening quote
                content_start = i

                # Find closing quote
                while i < len(text) and text[i] != "'":
                    i += 1

                # Extract content (without quotes)
                content = text[content_start:i]
                single_quoted_parts.append(content)

                # Replace with placeholder
                placeholder_idx = len(single_quoted_parts) - 1
                result.append(f'{self.SINGLE_QUOTE_PLACEHOLDER_PREFIX}{placeholder_idx:04d}')

                if i < len(text):
                    i += 1  # Skip closing quote
            else:
                result.append(char)
                i += 1

        return ''.join(result), single_quoted_parts

    def _restore_single_quoted(self, text: str, parts: List[str]) -> str:
        """
        Restore single-quoted content from placeholders.
        The quotes are preserved so that later parsing stages can correctly
        identify quoted content (e.g., for pipe and operator handling).
        """
        import re
        pattern = re.escape(self.SINGLE_QUOTE_PLACEHOLDER_PREFIX) + r'(\d{4})'

        def replace(match):
            idx = int(match.group(1))
            if idx < len(parts):
                # Restore with quotes preserved
                return "'" + parts[idx] + "'"
            return match.group(0)

        return re.sub(pattern, replace, text)

    def expand_variables_only(self, text: str) -> str:
        """
        Expand only variable references, not command substitution

        Useful for contexts where command substitution shouldn't happen.
        """
        # Protect single-quoted content from expansion
        text, single_quoted_parts = self._protect_single_quoted(text)

        text = self._expand_arithmetic(text)
        text = self._expand_parameters(text)

        # Restore single-quoted content
        text = self._restore_single_quoted(text, single_quoted_parts)
        return text

    def _expand_command_substitution(self, text: str) -> str:
        """Expand $(cmd) and `cmd` substitutions"""
        # First, protect escaped backticks
        ESCAPED_BACKTICK = '\ue001'
        text = text.replace('\\`', ESCAPED_BACKTICK)

        # Handle $(cmd) - process innermost first
        max_iterations = 10
        for _ in range(max_iterations):
            result = self._find_innermost_command_subst(text)
            if result is None:
                break
            start, end, command = result
            output = self._execute_command_substitution(command)
            text = text[:start] + output + text[end:]

        # Handle `cmd` (backticks) - only unescaped ones
        def replace_backtick(match):
            command = match.group(1)
            return self._execute_command_substitution(command)

        text = re.sub(r'`([^`]+)`', replace_backtick, text)

        # Restore escaped backticks
        text = text.replace(ESCAPED_BACKTICK, '`')

        return text

    def _find_innermost_command_subst(self, text: str) -> Optional[Tuple[int, int, str]]:
        """Find the innermost $(command) substitution"""
        tracker = QuoteTracker()
        i = 0

        while i < len(text) - 1:
            char = text[i]
            tracker.process_char(char)

            # Check if command substitution is allowed in current quote context
            if tracker.allows_command_substitution() and text[i:i+2] == '$(':
                # Skip if this is $((
                if i < len(text) - 2 and text[i:i+3] == '$((':
                    i += 1
                    continue

                # Found $(, find matching )
                start = i
                content, end = BracketMatcher.extract_balanced(text, i + 1, '(', ')')

                if end > i + 1:
                    # Check if there are nested $( inside
                    if '$(' in content and '$((' not in content:
                        # Has nested - recurse to find innermost
                        i += 2
                        continue
                    return (start, end, content)

            i += 1

        return None

    def _execute_command_substitution(self, command: str) -> str:
        """Execute a command and return its output"""
        # Delegate to shell's implementation
        return self.shell._execute_command_substitution(command)

    def _expand_arithmetic(self, text: str) -> str:
        """Expand $((expr)) arithmetic expressions, handling nesting"""
        # Process from innermost to outermost
        max_iterations = 10
        for _ in range(max_iterations):
            # Find innermost $((..))
            result = self._find_innermost_arithmetic(text)
            if result is None:
                break

            start, end, expr = result
            # Evaluate and replace
            value = self.arith_evaluator.evaluate(expr)
            text = text[:start] + str(value) + text[end:]

        return text

    def _find_innermost_arithmetic(self, text: str) -> Optional[Tuple[int, int, str]]:
        """Find the innermost $((expr)) for evaluation"""
        # Find all $(( positions
        i = 0
        candidates = []

        while i < len(text) - 2:
            if text[i:i+3] == '$((':
                candidates.append(i)
            i += 1

        if not candidates:
            return None

        # For each candidate, check if it's innermost (no nested $(( inside)
        for start in reversed(candidates):
            # Find matching ))
            depth = 2  # We've seen $((
            j = start + 3
            expr_start = j

            while j < len(text) and depth > 0:
                if text[j:j+3] == '$((':
                    depth += 2
                    j += 3
                    continue
                elif text[j:j+2] == '))' and depth >= 2:
                    depth -= 2
                    if depth == 0:
                        # Found matching ))
                        expr = text[expr_start:j]
                        # Check if this expression contains nested $((
                        if '$((' not in expr:
                            return (start, j + 2, expr)
                    j += 2
                    continue
                elif text[j] == '(':
                    depth += 1
                elif text[j] == ')':
                    depth -= 1
                j += 1

        # Try simpler approach: find first $(( without nested $((
        for start in candidates:
            depth = 2
            j = start + 3
            expr_start = j

            while j < len(text) and depth > 0:
                if text[j] == '(':
                    depth += 1
                elif text[j] == ')':
                    depth -= 1
                j += 1

            if depth == 0:
                expr = text[expr_start:j-2]
                if '$((' not in expr:
                    return (start, j, expr)

        return None

    def _expand_parameters(self, text: str) -> str:
        """Expand ${VAR} and $VAR parameter references"""
        # First, protect escaped dollars (\$) by replacing with placeholder
        # This handles cases like "cost: \$100" where \$ should be literal $
        ESCAPED_DOLLAR_PLACEHOLDER = '\ue000'
        text = text.replace('\\$', ESCAPED_DOLLAR_PLACEHOLDER)

        # Expand special variables
        text = text.replace('$?', self.shell._get_variable('?'))
        text = text.replace('$#', self.shell._get_variable('#'))
        text = text.replace('$@', self.shell._get_variable('@'))
        text = text.replace('$*', self.shell._get_variable('*'))
        text = text.replace('$0', self.shell._get_variable('0'))

        # Expand ${...} with modifiers
        text = self._expand_braced_parameters(text)

        # Expand $N (positional parameters)
        def replace_positional(match):
            return self.shell._get_variable(match.group(1))
        text = re.sub(r'\$(\d+)', replace_positional, text)

        # Expand $VAR (simple variables)
        def replace_simple(match):
            return self.shell._get_variable(match.group(1))
        text = re.sub(r'\$([A-Za-z_][A-Za-z0-9_]*)', replace_simple, text)

        # Restore escaped dollar
        text = text.replace(ESCAPED_DOLLAR_PLACEHOLDER, '$')

        return text

    def _expand_braced_parameters(self, text: str) -> str:
        """Expand ${...} parameter expansions with modifiers"""
        result = []
        i = 0

        while i < len(text):
            if i < len(text) - 1 and text[i:i+2] == '${':
                # Find matching }
                content, end = BracketMatcher.extract_balanced(text, i + 1, '{', '}')

                if end > i + 1:
                    # Parse and expand
                    expansion = self.param_expander.parse(content)
                    if expansion:
                        value = self.param_expander.expand(expansion)
                        result.append(value)
                    else:
                        # Invalid, keep original
                        result.append(text[i:end])
                    i = end
                else:
                    result.append(text[i])
                    i += 1
            else:
                result.append(text[i])
                i += 1

        return ''.join(result)
