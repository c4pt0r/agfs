"""
Comprehensive tests for lexer.py module.

Tests cover:
- TokenType: token type enumeration
- Token: token class with equality and representation
- ShellLexer: shell command tokenization
- QuoteTracker: quote and escape tracking
"""

import pytest
from agfs_shell.lexer import TokenType, Token, ShellLexer, QuoteTracker


# =============================================================================
# TokenType Tests
# =============================================================================

class TestTokenType:
    """Tests for TokenType enum."""

    def test_token_types_exist(self):
        """Test that all token types exist."""
        assert TokenType.WORD
        assert TokenType.PIPE
        assert TokenType.REDIRECT
        assert TokenType.COMMENT
        assert TokenType.EOF

    def test_token_type_values(self):
        """Test token type values."""
        assert TokenType.WORD.value == "word"
        assert TokenType.PIPE.value == "pipe"
        assert TokenType.REDIRECT.value == "redirect"
        assert TokenType.COMMENT.value == "comment"
        assert TokenType.EOF.value == "eof"


# =============================================================================
# Token Tests
# =============================================================================

class TestToken:
    """Tests for Token class."""

    def test_token_creation(self):
        """Test creating a token."""
        token = Token(TokenType.WORD, "hello")
        assert token.type == TokenType.WORD
        assert token.value == "hello"
        assert token.position == 0

    def test_token_with_position(self):
        """Test creating a token with position."""
        token = Token(TokenType.WORD, "world", position=5)
        assert token.type == TokenType.WORD
        assert token.value == "world"
        assert token.position == 5

    def test_token_repr(self):
        """Test token representation."""
        token = Token(TokenType.WORD, "test", position=10)
        repr_str = repr(token)
        assert "word" in repr_str
        assert "test" in repr_str
        assert "10" in repr_str

    def test_token_equality(self):
        """Test token equality."""
        token1 = Token(TokenType.WORD, "hello")
        token2 = Token(TokenType.WORD, "hello")
        token3 = Token(TokenType.WORD, "world")
        token4 = Token(TokenType.PIPE, "hello")

        assert token1 == token2
        assert token1 != token3
        assert token1 != token4
        assert token1 != "not a token"


# =============================================================================
# ShellLexer Tests
# =============================================================================

class TestShellLexer:
    """Tests for ShellLexer class."""

    def test_lexer_empty_string(self):
        """Test lexing empty string."""
        lexer = ShellLexer("")
        tokens = lexer.tokenize()
        assert len(tokens) == 1
        assert tokens[0].type == TokenType.EOF

    def test_lexer_simple_word(self):
        """Test lexing a simple word."""
        lexer = ShellLexer("hello")
        tokens = lexer.tokenize()
        assert len(tokens) == 2  # word + EOF
        assert tokens[0].type == TokenType.WORD
        assert tokens[0].value == "hello"
        assert tokens[1].type == TokenType.EOF

    def test_lexer_multiple_words(self):
        """Test lexing multiple words."""
        lexer = ShellLexer("echo hello world")
        tokens = lexer.tokenize()
        assert len(tokens) == 4  # 3 words + EOF
        assert tokens[0].value == "echo"
        assert tokens[1].value == "hello"
        assert tokens[2].value == "world"

    def test_lexer_single_quotes(self):
        """Test lexing single quoted strings."""
        lexer = ShellLexer("'hello world'")
        tokens = lexer.tokenize()
        assert len(tokens) == 2  # quoted word + EOF
        assert tokens[0].type == TokenType.WORD
        # Value should include quotes or processed content

    def test_lexer_double_quotes(self):
        """Test lexing double quoted strings."""
        lexer = ShellLexer('"hello world"')
        tokens = lexer.tokenize()
        assert len(tokens) == 2  # quoted word + EOF
        assert tokens[0].type == TokenType.WORD

    def test_lexer_pipe(self):
        """Test lexing pipe character."""
        lexer = ShellLexer("echo | cat")
        tokens = lexer.tokenize()
        # Should have: echo, |, cat, EOF
        assert any(t.type == TokenType.PIPE for t in tokens)

    def test_lexer_redirect(self):
        """Test lexing redirect operators."""
        lexer = ShellLexer("echo > file.txt")
        tokens = lexer.tokenize()
        # Should have redirect token
        assert any(t.type == TokenType.REDIRECT for t in tokens)

    def test_lexer_comment(self):
        """Test lexing comments."""
        lexer = ShellLexer("echo hello # this is a comment")
        tokens = lexer.tokenize()
        # Should have comment token
        assert any(t.type == TokenType.COMMENT for t in tokens)

    def test_lexer_whitespace_handling(self):
        """Test that whitespace is handled correctly."""
        lexer = ShellLexer("  echo   hello  ")
        tokens = lexer.tokenize()
        word_tokens = [t for t in tokens if t.type == TokenType.WORD]
        assert len(word_tokens) == 2
        assert word_tokens[0].value == "echo"
        assert word_tokens[1].value == "hello"

    def test_lexer_mixed_quotes(self):
        """Test mixing single and double quotes."""
        lexer = ShellLexer("echo 'hello' \"world\"")
        tokens = lexer.tokenize()
        word_tokens = [t for t in tokens if t.type == TokenType.WORD]
        assert len(word_tokens) == 3  # echo + 2 quoted words

    def test_lexer_escape_in_double_quotes(self):
        """Test escapes in double quotes."""
        lexer = ShellLexer('"hello\\"world"')
        tokens = lexer.tokenize()
        assert len(tokens) >= 1

    def test_lexer_unclosed_quote(self):
        """Test handling of unclosed quotes."""
        lexer = ShellLexer('"hello')
        tokens = lexer.tokenize()
        # Should handle gracefully
        assert len(tokens) >= 1


# =============================================================================
# QuoteTracker Tests
# =============================================================================

class TestQuoteTracker:
    """Tests for QuoteTracker class."""

    def test_tracker_initial_state(self):
        """Test quote tracker initial state."""
        tracker = QuoteTracker()
        assert not tracker.is_quoted()
        assert tracker.allows_variable_expansion()
        assert tracker.allows_command_substitution()
        assert tracker.allows_glob_expansion()

    def test_tracker_single_quote(self):
        """Test entering single quote."""
        tracker = QuoteTracker()
        tracker.process_char("'")
        assert tracker.is_quoted()

    def test_tracker_double_quote(self):
        """Test entering double quote."""
        tracker = QuoteTracker()
        tracker.process_char('"')
        assert tracker.is_quoted()

    def test_tracker_exit_quote(self):
        """Test exiting quotes."""
        tracker = QuoteTracker()
        tracker.process_char("'")
        assert tracker.is_quoted()
        tracker.process_char("'")
        assert not tracker.is_quoted()

    def test_tracker_variable_expansion_in_single_quotes(self):
        """Test that variable expansion is disabled in single quotes."""
        tracker = QuoteTracker()
        tracker.process_char("'")
        assert not tracker.allows_variable_expansion()

    def test_tracker_variable_expansion_in_double_quotes(self):
        """Test that variable expansion is allowed in double quotes."""
        tracker = QuoteTracker()
        tracker.process_char('"')
        assert tracker.allows_variable_expansion()

    def test_tracker_command_substitution_in_single_quotes(self):
        """Test that command substitution is disabled in single quotes."""
        tracker = QuoteTracker()
        tracker.process_char("'")
        assert not tracker.allows_command_substitution()

    def test_tracker_command_substitution_in_double_quotes(self):
        """Test that command substitution is allowed in double quotes."""
        tracker = QuoteTracker()
        tracker.process_char('"')
        assert tracker.allows_command_substitution()

    def test_tracker_glob_expansion(self):
        """Test glob expansion rules."""
        tracker = QuoteTracker()
        assert tracker.allows_glob_expansion()

        tracker.process_char("'")
        assert not tracker.allows_glob_expansion()

    def test_tracker_reset(self):
        """Test resetting tracker."""
        tracker = QuoteTracker()
        tracker.process_char("'")
        assert tracker.is_quoted()
        tracker.reset()
        assert not tracker.is_quoted()

    def test_tracker_escape_handling(self):
        """Test escape character handling."""
        tracker = QuoteTracker()
        tracker.process_char('\\')
        # Next character should be escaped
        tracker.process_char('"')
        # Should not enter quoted state
        assert not tracker.is_quoted()

    def test_tracker_complex_sequence(self):
        """Test complex quote sequence."""
        tracker = QuoteTracker()
        text = "echo 'hello' \"world\""

        for char in text:
            tracker.process_char(char)

        # Should end unquoted
        assert not tracker.is_quoted()


# =============================================================================
# Integration Tests
# =============================================================================

class TestLexerIntegration:
    """Integration tests for lexer."""

    def test_complex_command_line(self):
        """Test lexing complex command line."""
        lexer = ShellLexer("echo 'hello world' | grep hello > output.txt # comment")
        tokens = lexer.tokenize()

        # Should have multiple token types
        types = {t.type for t in tokens}
        assert TokenType.WORD in types
        assert TokenType.EOF in types

    def test_command_with_variables(self):
        """Test lexing command with variables."""
        lexer = ShellLexer("echo $HOME ${USER}")
        tokens = lexer.tokenize()

        # Should tokenize correctly
        word_tokens = [t for t in tokens if t.type == TokenType.WORD]
        assert len(word_tokens) >= 1

    def test_command_with_arithmetic(self):
        """Test lexing command with arithmetic."""
        lexer = ShellLexer("echo $((1 + 2))")
        tokens = lexer.tokenize()

        # Should tokenize correctly
        assert len(tokens) >= 2

    def test_command_with_redirects(self):
        """Test lexing command with multiple redirects."""
        lexer = ShellLexer("command < input.txt > output.txt 2>&1")
        tokens = lexer.tokenize()

        # Should have redirect tokens
        redirect_tokens = [t for t in tokens if t.type == TokenType.REDIRECT]
        assert len(redirect_tokens) >= 1


# =============================================================================
# Regression: lexer infinite-loop on `2>&1` and related operators (dev-2, 2026-05)
# =============================================================================
#
# Before the fix, ShellLexer.tokenize() hung forever on inputs containing
# bare `&` or `;` (notably `2>&1`): the existing redirection branch consumed
# `2>` correctly, then `&` fell through to read_word() which terminates on `&`
# without consuming it, leaving pos unchanged so the outer while-loop spun
# indefinitely. These tests exercise the new operator handling and a safety
# net that guarantees forward progress on any unhandled character.
#
# All regression tests carry an explicit pytest timeout so a future
# re-introduction of the hang fails fast instead of stalling CI.


def _values(tokens, type_filter=None):
    """Helper: project tokens to (type, value) tuples, dropping EOF."""
    if type_filter is None:
        return [(t.type, t.value) for t in tokens if t.type != TokenType.EOF]
    return [t.value for t in tokens if t.type == type_filter]


class TestLexerHangRegression:
    """Inputs that used to spin the lexer forever — now must complete promptly."""

    @pytest.mark.timeout(2)
    def test_2_stderr_to_stdout_does_not_hang(self):
        toks = ShellLexer("echo hi 2>&1").tokenize()
        assert _values(toks) == [
            (TokenType.WORD, "echo"),
            (TokenType.WORD, "hi"),
            (TokenType.REDIRECT, "2>&1"),
        ]

    @pytest.mark.timeout(2)
    def test_existing_integration_input_does_not_hang(self):
        # Same input used by TestLexerIntegration.test_command_with_redirects.
        # Pre-fix, this string caused tokenize() to hang.
        toks = ShellLexer("command < input.txt > output.txt 2>&1").tokenize()
        redirects = _values(toks, TokenType.REDIRECT)
        assert redirects == ["<", ">", "2>&1"]

    @pytest.mark.timeout(2)
    def test_background_amp_does_not_hang(self):
        toks = ShellLexer("sleep 5 &").tokenize()
        assert _values(toks) == [
            (TokenType.WORD, "sleep"),
            (TokenType.WORD, "5"),
            (TokenType.BACKGROUND, "&"),
        ]

    @pytest.mark.timeout(2)
    def test_semicolon_separator_does_not_hang(self):
        toks = ShellLexer("cmd1; cmd2").tokenize()
        assert _values(toks) == [
            (TokenType.WORD, "cmd1"),
            (TokenType.SEMICOLON, ";"),
            (TokenType.WORD, "cmd2"),
        ]

    @pytest.mark.timeout(2)
    def test_pipeline_with_2_stderr_to_stdout(self):
        toks = ShellLexer("echo a | wc -l 2>&1").tokenize()
        assert _values(toks) == [
            (TokenType.WORD, "echo"),
            (TokenType.WORD, "a"),
            (TokenType.PIPE, "|"),
            (TokenType.WORD, "wc"),
            (TokenType.WORD, "-l"),
            (TokenType.REDIRECT, "2>&1"),
        ]


class TestFdDuplicationRedirections:
    """N>&M, N>&-, <&N, &> and &>> should each tokenize as a single REDIRECT."""

    def test_2_to_1_duplication(self):
        toks = ShellLexer("cmd 2>&1").tokenize()
        assert _values(toks, TokenType.REDIRECT) == ["2>&1"]

    def test_1_to_2_duplication(self):
        toks = ShellLexer("cmd 1>&2").tokenize()
        assert _values(toks, TokenType.REDIRECT) == ["1>&2"]

    def test_close_fd_with_dash(self):
        toks = ShellLexer("cmd 2>&-").tokenize()
        assert _values(toks, TokenType.REDIRECT) == ["2>&-"]

    def test_dup_input_fd(self):
        toks = ShellLexer("cmd <&0").tokenize()
        assert _values(toks, TokenType.REDIRECT) == ["<&0"]

    def test_amp_redirect_both_streams(self):
        toks = ShellLexer("cmd &> out.log").tokenize()
        assert _values(toks) == [
            (TokenType.WORD, "cmd"),
            (TokenType.REDIRECT, "&>"),
            (TokenType.WORD, "out.log"),
        ]

    def test_amp_append_both_streams(self):
        toks = ShellLexer("cmd &>> out.log").tokenize()
        assert _values(toks) == [
            (TokenType.WORD, "cmd"),
            (TokenType.REDIRECT, "&>>"),
            (TokenType.WORD, "out.log"),
        ]

    def test_multi_digit_fd_redirection(self):
        toks = ShellLexer("cmd 10> file").tokenize()
        assert _values(toks, TokenType.REDIRECT) == ["10>"]

    def test_2_append_redirection(self):
        toks = ShellLexer("cmd 2>> err.log").tokenize()
        assert _values(toks, TokenType.REDIRECT) == ["2>>"]


class TestLogicalAndBackgroundOperators:
    """&&, ||, & (background), ; produce dedicated token types."""

    def test_and_or(self):
        toks = ShellLexer("cmd1 && cmd2 || cmd3").tokenize()
        assert _values(toks) == [
            (TokenType.WORD, "cmd1"),
            (TokenType.AND, "&&"),
            (TokenType.WORD, "cmd2"),
            (TokenType.OR, "||"),
            (TokenType.WORD, "cmd3"),
        ]

    def test_background_at_end(self):
        toks = ShellLexer("sleep 1 &").tokenize()
        types = [t.type for t in toks if t.type != TokenType.EOF]
        assert types[-1] == TokenType.BACKGROUND

    def test_semicolon_then_command(self):
        toks = ShellLexer("a; b; c").tokenize()
        types = [t.type for t in toks if t.type != TokenType.EOF]
        assert types == [
            TokenType.WORD,
            TokenType.SEMICOLON,
            TokenType.WORD,
            TokenType.SEMICOLON,
            TokenType.WORD,
        ]


class TestQuotesAndPlainNumbers:
    """Numbers in argument position stay WORDs; quoted operators stay literal."""

    def test_plain_number_is_word(self):
        toks = ShellLexer("echo 2 ok").tokenize()
        assert _values(toks) == [
            (TokenType.WORD, "echo"),
            (TokenType.WORD, "2"),
            (TokenType.WORD, "ok"),
        ]

    def test_quoted_redirection_text_stays_word(self):
        toks = ShellLexer('echo "keep 2>&1 inside quotes"').tokenize()
        assert _values(toks) == [
            (TokenType.WORD, "echo"),
            (TokenType.WORD, "keep 2>&1 inside quotes"),
        ]

    def test_quoted_amp_stays_word(self):
        toks = ShellLexer("echo 'a && b'").tokenize()
        assert _values(toks) == [
            (TokenType.WORD, "echo"),
            (TokenType.WORD, "a && b"),
        ]


class TestLexerForwardProgress:
    """Safety-net guarantee: tokenize() must always make forward progress."""

    @pytest.mark.timeout(2)
    def test_lone_amp_does_not_hang(self):
        toks = ShellLexer("&").tokenize()
        assert toks[-1].type == TokenType.EOF

    @pytest.mark.timeout(2)
    def test_lone_semicolon_does_not_hang(self):
        toks = ShellLexer(";").tokenize()
        assert toks[-1].type == TokenType.EOF

    @pytest.mark.timeout(2)
    def test_empty_input_eof_only(self):
        toks = ShellLexer("").tokenize()
        assert [t.type for t in toks] == [TokenType.EOF]

    @pytest.mark.timeout(2)
    def test_only_whitespace(self):
        toks = ShellLexer("   \t  ").tokenize()
        assert [t.type for t in toks] == [TokenType.EOF]

    @pytest.mark.timeout(2)
    def test_unhandled_chars_do_not_hang(self):
        # Backticks and other chars without a dedicated handler must still
        # let the lexer terminate, courtesy of the new safety net.
        toks = ShellLexer("foo `bar` baz").tokenize()
        assert toks[-1].type == TokenType.EOF


if __name__ == '__main__':
    pytest.main([__file__, '-v'])
