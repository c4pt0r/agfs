"""
Robust lexer for shell command parsing

This module provides a unified lexer that handles:
- Quote tracking (single and double quotes)
- Escape sequences
- Comment detection
- Token splitting

Replaces fragile manual character-by-character parsing throughout the codebase.
"""

from typing import List, Optional
from enum import Enum


class TokenType(Enum):
    """Types of tokens the lexer can produce"""
    WORD = "word"
    PIPE = "pipe"
    REDIRECT = "redirect"
    COMMENT = "comment"
    BACKGROUND = "background"   # & (run in background / pipeline terminator)
    SEMICOLON = "semicolon"     # ; (statement separator)
    AND = "and"                 # && (logical AND)
    OR = "or"                   # || (logical OR)
    EOF = "eof"


class Token:
    """A single lexical token"""

    def __init__(self, type: TokenType, value: str, position: int = 0):
        self.type = type
        self.value = value
        self.position = position

    def __repr__(self):
        return f"Token({self.type.value}, {repr(self.value)}, pos={self.position})"

    def __eq__(self, other):
        if not isinstance(other, Token):
            return False
        return self.type == other.type and self.value == other.value


class ShellLexer:
    """
    Robust lexer for shell commands

    Handles quotes, escapes, and special characters correctly.
    """

    def __init__(self, text: str):
        """
        Initialize lexer with text to parse

        Args:
            text: Shell command line to tokenize
        """
        self.text = text
        self.pos = 0
        self.length = len(text)

    def peek(self, offset: int = 0) -> Optional[str]:
        """Look ahead at character without consuming it"""
        pos = self.pos + offset
        if pos < self.length:
            return self.text[pos]
        return None

    def advance(self) -> Optional[str]:
        """Consume and return current character"""
        if self.pos < self.length:
            char = self.text[self.pos]
            self.pos += 1
            return char
        return None

    def skip_whitespace(self):
        """Skip over whitespace characters"""
        while self.peek() and self.peek() in ' \t':
            self.advance()

    def read_quoted_string(self, quote_char: str) -> str:
        """
        Read a quoted string, handling escapes

        Args:
            quote_char: Quote character (' or ")

        Returns:
            Content of quoted string (without quotes)
        """
        result = []
        # Skip opening quote
        self.advance()

        while True:
            char = self.peek()

            if char is None:
                # Unclosed quote - return what we have
                break

            if char == '\\' and quote_char == '"':
                # Escape sequence in double quotes
                self.advance()
                next_char = self.advance()
                if next_char:
                    result.append(next_char)
            elif char == quote_char:
                # Closing quote
                self.advance()
                break
            else:
                result.append(char)
                self.advance()

        return ''.join(result)

    def read_word(self) -> str:
        """
        Read a word token, respecting quotes and escapes

        Returns:
            Word content
        """
        result = []

        while True:
            char = self.peek()

            if char is None:
                break

            # Check for special characters that end a word
            if char in ' \t\n|<>;&':
                break

            # Handle quotes
            if char == '"':
                quoted = self.read_quoted_string('"')
                result.append(quoted)
            elif char == "'":
                quoted = self.read_quoted_string("'")
                result.append(quoted)
            # Handle escapes
            elif char == '\\':
                self.advance()
                next_char = self.advance()
                if next_char:
                    result.append(next_char)
            else:
                result.append(char)
                self.advance()

        return ''.join(result)

    def _consume(self) -> str:
        """Advance one character and return it as a str (empty at EOF)."""
        ch = self.advance()
        return ch if ch is not None else ''

    def _consume_fd_dup_suffix(self, redir: str) -> str:
        """
        If the next char is '&' followed by digits or '-', consume it as a
        file-descriptor duplication / close suffix (e.g. '>&1', '2>&1', '>&-').

        Returns the (possibly extended) redirection token.
        """
        if self.peek() != '&':
            return redir
        # Only treat '&' as a duplication marker when followed by a digit or '-'.
        # Otherwise '&' belongs to the next token (background, &&, &>).
        nxt = self.peek(1)
        if nxt is None or not (nxt.isdigit() or nxt == '-'):
            return redir
        redir += self._consume()  # consume '&'
        # Digits (file descriptor number) or '-' (close fd)
        if self.peek() == '-':
            redir += self._consume()
        else:
            nxt = self.peek()
            while nxt is not None and nxt.isdigit():
                redir += self._consume()
                nxt = self.peek()
        return redir

    def tokenize(self) -> List[Token]:
        """
        Tokenize the entire input

        Returns:
            List of tokens
        """
        tokens = []

        while self.pos < self.length:
            self.skip_whitespace()

            if self.pos >= self.length:
                break

            char = self.peek()
            start_pos = self.pos

            # Check for comments
            if char == '#':
                # Read to end of line
                comment = []
                while self.peek() and self.peek() != '\n':
                    comment.append(self.advance())
                tokens.append(Token(TokenType.COMMENT, ''.join(comment), start_pos))
                continue

            # Logical OR: ||
            if char == '|' and self.peek(1) == '|':
                self.advance()
                self.advance()
                tokens.append(Token(TokenType.OR, '||', start_pos))
                continue

            # Pipe: |
            if char == '|':
                self.advance()
                tokens.append(Token(TokenType.PIPE, '|', start_pos))
                continue

            # &> or &>> (redirect both stdout and stderr)
            if char == '&' and self.peek(1) == '>':
                redir = self._consume() + self._consume()  # '&>'
                if self.peek() == '>':
                    redir += self._consume()  # '&>>'
                tokens.append(Token(TokenType.REDIRECT, redir, start_pos))
                continue

            # Logical AND: &&
            if char == '&' and self.peek(1) == '&':
                self.advance()
                self.advance()
                tokens.append(Token(TokenType.AND, '&&', start_pos))
                continue

            # Background: &
            if char == '&':
                self.advance()
                tokens.append(Token(TokenType.BACKGROUND, '&', start_pos))
                continue

            # Statement separator: ;
            if char == ';':
                self.advance()
                tokens.append(Token(TokenType.SEMICOLON, ';', start_pos))
                continue

            # Output redirection: >, >>, >&N, >&-
            if char == '>':
                redir = self._consume()
                if self.peek() == '>':
                    redir += self._consume()
                redir = self._consume_fd_dup_suffix(redir)
                tokens.append(Token(TokenType.REDIRECT, redir, start_pos))
                continue

            # Input redirection: <, <<, <&N
            if char == '<':
                redir = self._consume()
                if self.peek() == '<':
                    redir += self._consume()
                redir = self._consume_fd_dup_suffix(redir)
                tokens.append(Token(TokenType.REDIRECT, redir, start_pos))
                continue

            # FD-prefixed redirection: NUM> NUM>> NUM>&M NUM>&-
            # Only consume the digit prefix if it is actually followed by a
            # redirection operator, so plain numeric words still tokenize as WORDs.
            if char is not None and char.isdigit():
                # Look past any consecutive digits for '>' or '<'
                lookahead = 1
                ahead = self.peek(lookahead)
                while ahead is not None and ahead.isdigit():
                    lookahead += 1
                    ahead = self.peek(lookahead)
                if self.peek(lookahead) in ('>', '<'):
                    redir = ''
                    for _ in range(lookahead):
                        redir += self._consume()
                    # Consume operator
                    redir += self._consume()  # '>' or '<'
                    if self.peek() == redir[-1]:  # '>>' or '<<'
                        redir += self._consume()
                    redir = self._consume_fd_dup_suffix(redir)
                    tokens.append(Token(TokenType.REDIRECT, redir, start_pos))
                    continue

            # Otherwise, read a word
            word = self.read_word()
            if word:
                tokens.append(Token(TokenType.WORD, word, start_pos))
                continue

            # Safety net: ensure forward progress so we never loop forever on
            # an unrecognized character. read_word may stop at a special char
            # without consuming it; if nothing else advanced pos either, skip
            # the offending byte rather than spin.
            if self.pos == start_pos:
                self.advance()

        tokens.append(Token(TokenType.EOF, '', self.pos))
        return tokens


class QuoteTracker:
    """
    Utility class to track quote state while parsing

    Use this when you need to manually parse but need to know if you're inside quotes.
    """

    def __init__(self):
        self.in_single_quote = False
        self.in_double_quote = False
        self.escape_next = False

    def process_char(self, char: str):
        """
        Update quote state based on character

        Args:
            char: Current character being processed
        """
        if self.escape_next:
            self.escape_next = False
            return

        # Backslash only escapes outside single quotes
        # In single quotes, backslash is a literal character (Bash behavior)
        if char == '\\' and not self.in_single_quote:
            self.escape_next = True
            return

        if char == '"' and not self.in_single_quote:
            self.in_double_quote = not self.in_double_quote
        elif char == "'" and not self.in_double_quote:
            self.in_single_quote = not self.in_single_quote

    def is_quoted(self) -> bool:
        """Check if currently inside any type of quotes"""
        return self.in_single_quote or self.in_double_quote

    def allows_variable_expansion(self) -> bool:
        """
        Check if variable expansion ($VAR) is allowed in current context.

        Bash behavior:
        - Single quotes: NO expansion (literal text)
        - Double quotes: YES expansion
        - Unquoted: YES expansion
        """
        return not self.in_single_quote

    def allows_command_substitution(self) -> bool:
        """
        Check if command substitution $(cmd) is allowed in current context.

        Bash behavior:
        - Single quotes: NO substitution (literal text)
        - Double quotes: YES substitution
        - Unquoted: YES substitution
        """
        return not self.in_single_quote

    def allows_glob_expansion(self) -> bool:
        """
        Check if glob/wildcard expansion (*, ?) is allowed in current context.

        Bash behavior:
        - Single quotes: NO expansion (literal text)
        - Double quotes: NO expansion (literal text)
        - Unquoted: YES expansion
        """
        return not self.in_single_quote and not self.in_double_quote

    def reset(self):
        """Reset quote tracking state"""
        self.in_single_quote = False
        self.in_double_quote = False
        self.escape_next = False


def strip_comments(line: str, comment_chars: str = '#') -> str:
    """
    Strip comments from a line, respecting quotes

    Args:
        line: Line to process
        comment_chars: Characters that start comments (default: '#')

    Returns:
        Line with comments removed

    Example:
        >>> strip_comments('echo "test # not a comment" # real comment')
        'echo "test # not a comment" '
    """
    tracker = QuoteTracker()
    result = []

    for i, char in enumerate(line):
        tracker.process_char(char)

        # Check if this starts a comment (when not quoted)
        if char in comment_chars and not tracker.is_quoted():
            break

        result.append(char)

    return ''.join(result).rstrip()


def split_respecting_quotes(text: str, delimiter: str) -> List[str]:
    """
    Split text by delimiter, but only when not inside quotes

    This is a utility function that uses QuoteTracker.
    For more complex parsing, use ShellLexer instead.

    Args:
        text: Text to split
        delimiter: Delimiter to split on

    Returns:
        List of parts

    Example:
        >>> split_respecting_quotes('echo "a | b" | wc', '|')
        ['echo "a | b" ', ' wc']
    """
    tracker = QuoteTracker()
    parts = []
    current = []
    i = 0

    while i < len(text):
        char = text[i]
        tracker.process_char(char)

        # Check for delimiter when not quoted
        if not tracker.is_quoted() and text[i:i+len(delimiter)] == delimiter:
            parts.append(''.join(current))
            current = []
            i += len(delimiter)
        else:
            current.append(char)
            i += 1

    if current:
        parts.append(''.join(current))

    return parts
