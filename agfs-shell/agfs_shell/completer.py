"""Tab completion support for agfs-shell"""

import os
import shlex
from typing import List, Optional
from .builtins import BUILTINS
from .filesystem import AGFSFileSystem


class ShellCompleter:
    """Tab completion for shell commands and AGFS paths"""

    def __init__(self, filesystem: AGFSFileSystem):
        self.filesystem = filesystem
        self.command_names = sorted(BUILTINS.keys())
        self.matches = []
        self.shell = None  # Will be set by shell to access cwd

    def complete(self, text: str, state: int) -> Optional[str]:
        """
        Readline completion function

        Args:
            text: The text to complete
            state: The completion state (0 for first call, increments for each match)

        Returns:
            The next completion match, or None when no more matches
        """
        if state == 0:
            # First call - generate new matches
            import readline
            line = readline.get_line_buffer()
            begin_idx = readline.get_begidx()
            end_idx = readline.get_endidx()

            # Determine if we're completing a command or a path
            if begin_idx == 0 or line[:begin_idx].strip() == '':
                # Beginning of line - complete command names
                self.matches = self._complete_command(text)
            else:
                # Middle of line - complete paths
                self.matches = self._complete_path(text)

        # Return the next match
        if state < len(self.matches):
            return self.matches[state]
        return None

    def _complete_command(self, text: str) -> List[str]:
        """Complete command names"""
        if not text:
            return self.command_names

        matches = [cmd for cmd in self.command_names if cmd.startswith(text)]
        return matches

    def _needs_quoting(self, path: str) -> bool:
        """Check if a path needs to be quoted"""
        # Characters that require quoting in shell
        special_chars = ' \t\n|&;<>()$`\\"\''
        return any(c in path for c in special_chars)

    def _quote_if_needed(self, path: str) -> str:
        """Quote a path if it contains spaces or special characters"""
        if self._needs_quoting(path):
            # Use shlex.quote for proper shell quoting
            return shlex.quote(path)
        return path

    def _complete_path(self, text: str) -> List[str]:
        """Complete AGFS paths"""
        # Get current working directory
        cwd = self.shell.cwd if self.shell else '/'

        # Track if the text starts with a quote
        quote_char = None
        if text and text[0] in ('"', "'"):
            quote_char = text[0]
            text = text[1:]  # Remove the leading quote for path matching

        # Handle empty text - list current directory
        if not text:
            text = '.'

        # Resolve relative paths
        if text.startswith('/'):
            # Absolute path
            full_text = text
        else:
            # Relative path - resolve against cwd
            full_text = os.path.join(cwd, text)
            full_text = os.path.normpath(full_text)

        # Split path into directory and partial filename
        if full_text.endswith('/'):
            # Directory path - list contents
            directory = full_text
            partial = ''
        else:
            # Partial path - split into dir and filename
            directory = os.path.dirname(full_text)
            partial = os.path.basename(full_text)

            # Handle current directory
            if not directory or directory == '.':
                directory = cwd
            elif not directory.startswith('/'):
                directory = os.path.join(cwd, directory)
                directory = os.path.normpath(directory)

        # Get directory listing from AGFS
        try:
            entries = self.filesystem.list_directory(directory)

            # Determine if we should return relative or absolute paths
            return_relative = not text.startswith('/')

            # Filter by partial match and construct paths
            matches = []
            for entry in entries:
                name = entry.get('name', '')
                if name and name.startswith(partial):
                    # Construct absolute path
                    if directory == '/':
                        abs_path = f"/{name}"
                    else:
                        # Remove trailing slash from directory before joining
                        dir_clean = directory.rstrip('/')
                        abs_path = f"{dir_clean}/{name}"

                    # Add trailing slash for directories
                    if entry.get('type') == 'directory':
                        abs_path += '/'

                    # Convert to relative path if needed
                    final_path = None
                    if return_relative and cwd != '/':
                        # Make path relative to cwd
                        if abs_path.startswith(cwd + '/'):
                            final_path = abs_path[len(cwd) + 1:]
                        elif abs_path == cwd:
                            final_path = '.'
                        else:
                            # Path not under cwd, use absolute
                            final_path = abs_path
                    else:
                        final_path = abs_path

                    # Quote the path if needed
                    if quote_char:
                        # User started with a quote, so add matching quote
                        # Don't use shlex.quote as user already provided quote
                        final_path = f"{quote_char}{final_path}{quote_char}"
                    else:
                        # Auto-quote if the path needs it
                        final_path = self._quote_if_needed(final_path)

                    matches.append(final_path)

            return sorted(matches)
        except Exception:
            # If directory listing fails, return no matches
            return []
