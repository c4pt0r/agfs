"""
Comprehensive tests for expression.py module.

Tests cover:
- EscapeHandler: escape sequence processing
- ExpressionExpander: complete expression expansion (integration tests)
- Variable expansion, arithmetic, command substitution
"""

import pytest
from agfs_shell.expression import EscapeHandler, ExpressionExpander


# =============================================================================
# EscapeHandler Tests
# =============================================================================

class TestEscapeHandler:
    """Tests for EscapeHandler class."""

    def test_simple_escapes(self):
        """Test simple escape sequences like \\n, \\t."""
        assert EscapeHandler.process_escapes('hello\\nworld') == 'hello\nworld'
        assert EscapeHandler.process_escapes('tab\\there') == 'tab\there'
        assert EscapeHandler.process_escapes('\\r\\n') == '\r\n'

    def test_special_characters(self):
        """Test escape sequences for special characters."""
        assert EscapeHandler.process_escapes('\\a') == '\a'  # alert
        assert EscapeHandler.process_escapes('\\b') == '\b'  # backspace
        assert EscapeHandler.process_escapes('\\f') == '\f'  # form feed
        assert EscapeHandler.process_escapes('\\v') == '\v'  # vertical tab
        assert EscapeHandler.process_escapes('\\e') == '\x1b'  # escape

    def test_quote_escapes(self):
        """Test escaping quotes."""
        assert EscapeHandler.process_escapes("\\'") == "'"
        assert EscapeHandler.process_escapes('\\"') == '"'
        assert EscapeHandler.process_escapes('\\\\') == '\\'

    def test_hex_escapes(self):
        """Test hexadecimal escape sequences."""
        assert EscapeHandler.process_escapes('\\x41') == 'A'  # 0x41 = 65 = 'A'
        assert EscapeHandler.process_escapes('\\x20') == ' '  # space
        assert EscapeHandler.process_escapes('\\x00') == '\x00'  # null

    def test_null_character(self):
        """Test \\0 escape."""
        assert EscapeHandler.process_escapes('\\0') == '\x00'

    def test_no_escapes(self):
        """Test text without escapes."""
        assert EscapeHandler.process_escapes('hello world') == 'hello world'
        assert EscapeHandler.process_escapes('') == ''

    def test_mixed_escapes(self):
        """Test mixed escape sequences."""
        text = 'line1\\nline2\\ttab\\x41end'
        expected = 'line1\nline2\ttabAend'
        assert EscapeHandler.process_escapes(text) == expected


# =============================================================================
# ExpressionExpander Integration Tests
# =============================================================================

class TestExpressionExpander:
    """Tests for ExpressionExpander class (integration)."""

    def test_expand_simple_variable(self, mock_filesystem):
        """Test expanding $VAR."""
        from agfs_shell.shell import Shell
        from unittest.mock import patch

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()
            shell.env['USER'] = 'testuser'

            expander = ExpressionExpander(shell)
            result = expander.expand('Hello $USER')
            assert result == 'Hello testuser'

    def test_expand_braced_variable(self, mock_filesystem):
        """Test expanding ${VAR}."""
        from agfs_shell.shell import Shell
        from unittest.mock import patch

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()
            shell.env['VAR'] = 'value'

            expander = ExpressionExpander(shell)
            result = expander.expand('${VAR}')
            assert result == 'value'

    def test_expand_arithmetic(self, mock_filesystem):
        """Test expanding $((arithmetic))."""
        from agfs_shell.shell import Shell
        from unittest.mock import patch

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            expander = ExpressionExpander(shell)
            result = expander.expand('Result: $((2 + 3))')
            assert result == 'Result: 5'

    def test_expand_arithmetic_multiplication(self, mock_filesystem):
        """Test arithmetic multiplication."""
        from agfs_shell.shell import Shell
        from unittest.mock import patch

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            expander = ExpressionExpander(shell)
            result = expander.expand('Result: $((3 * 4))')
            assert result == 'Result: 12'

    def test_expand_arithmetic_subtraction(self, mock_filesystem):
        """Test arithmetic subtraction."""
        from agfs_shell.shell import Shell
        from unittest.mock import patch

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            expander = ExpressionExpander(shell)
            result = expander.expand('$((10 - 3))')
            assert result == '7'

    def test_expand_arithmetic_division(self, mock_filesystem):
        """Test arithmetic division."""
        from agfs_shell.shell import Shell
        from unittest.mock import patch

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            expander = ExpressionExpander(shell)
            result = expander.expand('$((15 / 3))')
            assert result == '5'

    def test_expand_arithmetic_complex(self, mock_filesystem):
        """Test complex arithmetic."""
        from agfs_shell.shell import Shell
        from unittest.mock import patch

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            expander = ExpressionExpander(shell)
            result = expander.expand('$((2 + 3 * 4))')
            assert result == '14'

    def test_expand_command_substitution(self, mock_filesystem):
        """Test expanding $(command)."""
        from agfs_shell.shell import Shell
        from unittest.mock import patch

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            expander = ExpressionExpander(shell)
            # Command substitution executes echo
            result = expander.expand('$(echo hello)')
            assert 'hello' in result

    def test_expand_mixed_expressions(self, mock_filesystem):
        """Test expanding mixed expressions."""
        from agfs_shell.shell import Shell
        from unittest.mock import patch

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()
            shell.env['VAR'] = 'value'

            expander = ExpressionExpander(shell)
            result = expander.expand('$VAR and $((1+1))')
            assert 'value' in result
            assert '2' in result

    def test_expand_multiple_variables(self, mock_filesystem):
        """Test expanding multiple variables."""
        from agfs_shell.shell import Shell
        from unittest.mock import patch

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()
            shell.env['A'] = 'first'
            shell.env['B'] = 'second'

            expander = ExpressionExpander(shell)
            result = expander.expand('$A-$B')
            assert result == 'first-second'

    def test_expand_undefined_variable(self, mock_filesystem):
        """Test expanding undefined variable."""
        from agfs_shell.shell import Shell
        from unittest.mock import patch

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            expander = ExpressionExpander(shell)
            result = expander.expand('$UNDEFINED')
            assert result == ''

    def test_expand_variable_with_default(self, mock_filesystem):
        """Test ${VAR:-default} expansion."""
        from agfs_shell.shell import Shell
        from unittest.mock import patch

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            expander = ExpressionExpander(shell)
            result = expander.expand('${UNDEFINED:-defaultvalue}')
            assert result == 'defaultvalue'

    def test_expand_variable_with_default_when_set(self, mock_filesystem):
        """Test ${VAR:-default} when VAR is set."""
        from agfs_shell.shell import Shell
        from unittest.mock import patch

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()
            shell.env['VAR'] = 'actual'

            expander = ExpressionExpander(shell)
            result = expander.expand('${VAR:-default}')
            assert result == 'actual'

    def test_expand_special_variables(self, mock_filesystem):
        """Test special variables like $?."""
        from agfs_shell.shell import Shell
        from unittest.mock import patch

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()
            shell.env['?'] = '0'

            expander = ExpressionExpander(shell)
            result = expander.expand('Exit: $?')
            assert 'Exit: 0' in result

    def test_expand_arithmetic_with_variables(self, mock_filesystem):
        """Test arithmetic with variable expansion."""
        from agfs_shell.shell import Shell
        from unittest.mock import patch

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()
            shell.env['X'] = '5'
            shell.env['Y'] = '3'

            expander = ExpressionExpander(shell)
            result = expander.expand('$(($X + $Y))')
            assert result == '8'

    def test_expand_nested_braces(self, mock_filesystem):
        """Test nested ${} expansion."""
        from agfs_shell.shell import Shell
        from unittest.mock import patch

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()
            shell.env['VAR'] = 'test'

            expander = ExpressionExpander(shell)
            result = expander.expand('prefix${VAR}suffix')
            assert result == 'prefixtestsuffix'

    def test_expand_backticks(self, mock_filesystem):
        """Test backtick command substitution."""
        from agfs_shell.shell import Shell
        from unittest.mock import patch

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            expander = ExpressionExpander(shell)
            result = expander.expand('`echo test`')
            assert 'test' in result

    def test_expand_empty_string(self, mock_filesystem):
        """Test expanding empty string."""
        from agfs_shell.shell import Shell
        from unittest.mock import patch

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            expander = ExpressionExpander(shell)
            result = expander.expand('')
            assert result == ''

    def test_expand_no_substitutions(self, mock_filesystem):
        """Test text with no substitutions."""
        from agfs_shell.shell import Shell
        from unittest.mock import patch

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            expander = ExpressionExpander(shell)
            result = expander.expand('plain text')
            assert result == 'plain text'


# =============================================================================
# Python 3.14 forward-compat: ast.Num removal regression
# =============================================================================

class TestArithmeticPython314Compat:
    """Pin the absence of the deprecated ``ast.Num`` branch.

    Background: ``agfs_shell/expression.py`` used to fall back to
    ``isinstance(node, ast.Num)`` with a ``hasattr(ast, 'Num')`` guard
    that was a no-op in Python 3.8+ (because ``ast.Constant`` already
    catches all numeric literals) and emitted a DeprecationWarning on
    every attribute lookup. Python 3.14 removes ``ast.Num`` outright.

    These tests pin the new contract: arithmetic still works, and the
    ``ast.Num`` DeprecationWarning no longer appears.
    """

    def _arith(self, mock_filesystem, expression):
        """Run ``$((expression))`` through the expander and return the
        result string."""
        import re
        from agfs_shell.shell import Shell
        from unittest.mock import patch

        with patch("agfs_shell.shell.AGFSFileSystem", return_value=mock_filesystem):
            shell = Shell()
            expander = ExpressionExpander(shell)
            out = expander.expand(f"$(({expression}))")
            # `expand` returns either the bare result for a sole
            # `$((...))` token or the expression embedded in text; we
            # always wrap in `$((...))` so the result is the bare
            # arithmetic value.
            return out.strip()

    def test_arithmetic_still_works(self, mock_filesystem):
        # The same numeric constants that used to traverse the
        # ``ast.Num`` branch on Python <3.8 now exclusively flow
        # through ``ast.Constant``.
        assert self._arith(mock_filesystem, "2 + 3") == "5"
        assert self._arith(mock_filesystem, "10 / 2") == "5"
        assert self._arith(mock_filesystem, "(1 + 2) * 3") == "9"
        assert self._arith(mock_filesystem, "-(-7)") == "7"

    def test_arithmetic_emits_no_ast_num_deprecation_warning(self, mock_filesystem, recwarn):
        # Exercise every operator path so any lingering ``ast.Num``
        # reference would fire.
        for expr in ["1 + 1", "5 - 2", "3 * 4", "8 / 2", "9 % 4", "2 ** 3", "-5"]:
            self._arith(mock_filesystem, expr)

        offending = [
            w for w in recwarn.list
            if issubclass(w.category, DeprecationWarning)
            and "ast.Num" in str(w.message)
        ]
        assert not offending, (
            "ast.Num DeprecationWarning leaked after the Python 3.14 "
            f"compat fix: {[str(w.message) for w in offending]}"
        )

    def test_no_executable_ast_num_reference(self):
        """Belt-and-braces: prove no *executable* ``ast.Num`` access
        remains. A future "Python 3.7 compat" revert that re-adds the
        deprecated branch would re-introduce the runtime lookup here
        and fail this assertion without needing the right Python
        version installed.

        We parse the source and look for attribute accesses on the
        ``ast`` module rather than grep, so docstrings and comments
        that explain *why* the branch was removed don't count as
        executable references.
        """
        import ast as _stdlib_ast
        import inspect
        from agfs_shell import expression

        tree = _stdlib_ast.parse(inspect.getsource(expression))
        offending = [
            node
            for node in _stdlib_ast.walk(tree)
            if isinstance(node, _stdlib_ast.Attribute)
            and isinstance(node.value, _stdlib_ast.Name)
            and node.value.id == "ast"
            and node.attr == "Num"
        ]
        assert not offending, (
            "executable ast.Num reference re-appeared in expression.py "
            "— Python 3.14 removes ast.Num entirely, so this would "
            "break arithmetic expansion on that interpreter."
        )


if __name__ == '__main__':
    pytest.main([__file__, '-v'])
