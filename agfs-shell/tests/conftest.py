"""
Pytest configuration and shared fixtures for agfs-shell tests.

This module provides reusable test fixtures for:
- Mock filesystem implementations
- Mock shell instances
- Test data and helper utilities
"""

import pytest
import io
from typing import Dict, List, Any, Iterator, Union, Optional
from unittest.mock import Mock


# ============================================================================
# Mock Filesystem Implementation
# ============================================================================

class MockFileSystem:
    """
    Mock filesystem for testing without AGFS server dependency.

    Provides an in-memory filesystem that simulates AGFS operations.
    """

    def __init__(self):
        self.files: Dict[str, bytes] = {}
        self.directories: set = {'/'}
        self.metadata: Dict[str, Dict[str, Any]] = {
            '/': {
                'name': '/',
                'type': 'directory',
                'size': 0,
                'mode': 0o755,
                'mtime': 1234567890,
            }
        }

    def read_file(self, path: str, offset: int = 0, size: int = -1,
                  stream: bool = False) -> Union[bytes, Iterator[bytes]]:
        """Read file content."""
        if path not in self.files:
            raise FileNotFoundError(f"File not found: {path}")

        if path in self.directories:
            raise IsADirectoryError(f"Is a directory: {path}")

        content = self.files[path]
        if size == -1:
            content = content[offset:]
        else:
            content = content[offset:offset + size]

        if stream:
            # Return iterator of chunks
            chunk_size = 8192
            return (content[i:i+chunk_size] for i in range(0, len(content), chunk_size))
        else:
            return content

    def write_file(self, path: str, data: Union[bytes, Iterator[bytes], io.IOBase],
                   append: bool = False) -> Optional[str]:
        """Write data to file."""
        # Handle different data types
        if isinstance(data, bytes):
            content = data
        elif isinstance(data, io.IOBase):
            content = data.read()
            if isinstance(content, str):
                content = content.encode('utf-8')
        else:
            # Iterator
            chunks = []
            for chunk in data:
                if isinstance(chunk, str):
                    chunk = chunk.encode('utf-8')
                chunks.append(chunk)
            content = b''.join(chunks)

        # Ensure parent directory exists
        parent = path.rsplit('/', 1)[0] or '/'
        if parent not in self.directories and parent != '/':
            raise FileNotFoundError(f"Parent directory not found: {parent}")

        # Write file
        if append and path in self.files:
            self.files[path] += content
        else:
            self.files[path] = content

        # Update metadata
        self.metadata[path] = {
            'name': path.split('/')[-1],
            'type': 'file',
            'size': len(self.files[path]),
            'mode': 0o644,
            'mtime': 1234567890,
        }

        return None

    def list_directory(self, path: str) -> List[Dict[str, Any]]:
        """List directory contents."""
        if path not in self.directories:
            raise FileNotFoundError(f"Directory not found: {path}")

        if not path.endswith('/'):
            path += '/'

        entries = []
        for item_path in list(self.files.keys()) + list(self.directories):
            if item_path.startswith(path) and item_path != path:
                # Only direct children
                relative = item_path[len(path):]
                if '/' not in relative.rstrip('/'):
                    entries.append(self.metadata.get(item_path, {
                        'name': relative.rstrip('/'),
                        'type': 'directory' if item_path in self.directories else 'file',
                        'size': len(self.files.get(item_path, b'')),
                        'mode': 0o755 if item_path in self.directories else 0o644,
                        'mtime': 1234567890,
                    }))

        return entries

    def get_file_info(self, path: str) -> Dict[str, Any]:
        """Get file metadata."""
        if path in self.metadata:
            return self.metadata[path]

        if path in self.files:
            return {
                'name': path.split('/')[-1],
                'type': 'file',
                'size': len(self.files[path]),
                'mode': 0o644,
                'mtime': 1234567890,
            }

        raise FileNotFoundError(f"File not found: {path}")

    def delete_file(self, path: str, recursive: bool = False) -> None:
        """Delete file or directory."""
        if path in self.files:
            del self.files[path]
            if path in self.metadata:
                del self.metadata[path]
        elif path in self.directories:
            if not recursive:
                # Check if directory is empty
                contents = self.list_directory(path)
                if contents:
                    raise OSError(f"Directory not empty: {path}")

            # Remove directory and all contents
            self.directories.discard(path)
            if path in self.metadata:
                del self.metadata[path]

            # Remove all children if recursive
            if recursive:
                if not path.endswith('/'):
                    path += '/'
                to_remove = [p for p in self.files if p.startswith(path)]
                for p in to_remove:
                    del self.files[p]
                    self.metadata.pop(p, None)

                dirs_to_remove = [d for d in self.directories if d.startswith(path)]
                for d in dirs_to_remove:
                    self.directories.discard(d)
                    self.metadata.pop(d, None)
        else:
            raise FileNotFoundError(f"File not found: {path}")

    def create_directory(self, path: str, parents: bool = False) -> None:
        """Create directory."""
        if path in self.directories or path in self.files:
            raise FileExistsError(f"File exists: {path}")

        parent = path.rsplit('/', 1)[0] or '/'
        if parent not in self.directories and not parents:
            raise FileNotFoundError(f"Parent directory not found: {parent}")

        # Create parent directories if needed
        if parents:
            parts = path.strip('/').split('/')
            current = ''
            for part in parts[:-1]:
                current += '/' + part
                if current not in self.directories:
                    self.directories.add(current)
                    self.metadata[current] = {
                        'name': part,
                        'type': 'directory',
                        'size': 0,
                        'mode': 0o755,
                        'mtime': 1234567890,
                    }

        self.directories.add(path)
        self.metadata[path] = {
            'name': path.split('/')[-1],
            'type': 'directory',
            'size': 0,
            'mode': 0o755,
            'mtime': 1234567890,
        }

    def exists(self, path: str) -> bool:
        """Check if path exists."""
        return path in self.files or path in self.directories

    def is_directory(self, path: str) -> bool:
        """Check if path is directory."""
        return path in self.directories

    def copy_file(self, src: str, dst: str) -> None:
        """Copy file."""
        if src not in self.files:
            raise FileNotFoundError(f"Source file not found: {src}")

        self.files[dst] = self.files[src]
        self.metadata[dst] = self.metadata[src].copy()
        self.metadata[dst]['name'] = dst.split('/')[-1]

    def move_file(self, src: str, dst: str) -> None:
        """Move/rename file."""
        if src not in self.files and src not in self.directories:
            raise FileNotFoundError(f"Source not found: {src}")

        if src in self.files:
            self.files[dst] = self.files[src]
            del self.files[src]

        if src in self.metadata:
            self.metadata[dst] = self.metadata[src].copy()
            self.metadata[dst]['name'] = dst.split('/')[-1]
            del self.metadata[src]

    def create_symlink(self, target: str, link: str) -> None:
        """Create symbolic link."""
        self.metadata[link] = {
            'name': link.split('/')[-1],
            'type': 'symlink',
            'target': target,
            'size': 0,
            'mode': 0o777,
            'mtime': 1234567890,
        }

    def readlink(self, path: str) -> str:
        """Read symbolic link target."""
        if path not in self.metadata or self.metadata[path].get('type') != 'symlink':
            raise OSError(f"Not a symlink: {path}")

        return self.metadata[path]['target']


# ============================================================================
# Pytest Fixtures
# ============================================================================

@pytest.fixture
def mock_filesystem():
    """
    Provides a mock filesystem for testing.

    Returns:
        MockFileSystem: In-memory filesystem instance

    Example:
        def test_read_file(mock_filesystem):
            mock_filesystem.write_file('/test.txt', b'Hello')
            content = mock_filesystem.read_file('/test.txt')
            assert content == b'Hello'
    """
    fs = MockFileSystem()

    # Add some default test files
    fs.write_file('/test.txt', b'Hello, World!')
    fs.write_file('/numbers.txt', b'1\n2\n3\n4\n5\n')
    fs.create_directory('/testdir')
    fs.write_file('/testdir/file1.txt', b'File 1 content')
    fs.write_file('/testdir/file2.txt', b'File 2 content')

    return fs


@pytest.fixture
def mock_shell(mock_filesystem):
    """
    Provides a mock shell instance for testing.

    Returns:
        Mock: Mock shell with common attributes

    Example:
        def test_command(mock_shell):
            result = some_command(mock_shell)
            assert result == 0
    """
    from agfs_shell.shell import Shell

    # Create mock shell
    shell = Mock(spec=Shell)
    shell.cwd = '/'
    shell.env = {
        'PATH': '/bin:/usr/bin',
        'HOME': '/home/test',
        'USER': 'test',
        '?': '0',
    }
    shell.functions = {}
    shell.aliases = {}
    shell.local_scopes = []
    shell.filesystem = mock_filesystem
    shell.console = Mock()

    return shell


@pytest.fixture
def test_data_dir(tmp_path):
    """
    Provides a temporary directory with test data files.

    Returns:
        pathlib.Path: Path to temporary test directory

    Example:
        def test_with_files(test_data_dir):
            test_file = test_data_dir / "test.txt"
            assert test_file.exists()
    """
    # Create test files
    (tmp_path / "test.txt").write_text("Hello, World!")
    (tmp_path / "numbers.txt").write_text("1\n2\n3\n4\n5\n")
    (tmp_path / "empty.txt").write_text("")

    # Create subdirectory
    subdir = tmp_path / "subdir"
    subdir.mkdir()
    (subdir / "file1.txt").write_text("File 1")
    (subdir / "file2.txt").write_text("File 2")

    return tmp_path


@pytest.fixture
def capture_output():
    """
    Provides buffer-backed OutputStream/ErrorStream for capturing command output.

    The streams are constructed via ``.to_buffer()`` so that
    ``stream.get_value()`` and ``process.get_stdout()`` /
    ``process.get_stderr()`` return what was written. Constructing
    ``OutputStream(io.BytesIO())`` directly routes writes to
    ``Stream._file`` instead of ``Stream._buffer``, which makes
    ``get_value()`` always return ``b''`` — the symptom that used to make
    ``test_write_to_stdout`` / ``test_write_to_stderr`` fail.

    Returns:
        tuple: (stdout, stderr) — both ``Stream`` instances whose contents
        can be read back with ``.get_value()``.

    Example:
        def test_command_output(capture_output):
            stdout, stderr = capture_output
            process = Process(stdout=stdout, stderr=stderr)
            # ... run command ...
            assert b"expected output" in stdout.get_value()
    """
    from agfs_shell.streams import OutputStream, ErrorStream

    return OutputStream.to_buffer(), ErrorStream.to_buffer()


@pytest.fixture
def mock_process(mock_filesystem, capture_output):
    """
    Provides a mock Process instance for command testing.

    Returns:
        Process: Process instance with mock filesystem and captured output

    Example:
        def test_cat_command(mock_process):
            mock_process.filesystem.write_file('/test.txt', b'content')
            result = cmd_cat(mock_process)
            assert result == 0
    """
    from agfs_shell.process import Process
    from agfs_shell.streams import InputStream

    stdout, stderr = capture_output
    stdin = InputStream.from_bytes(b'')

    process = Process(
        command='test',
        args=[],
        stdin=stdin,
        stdout=stdout,
        stderr=stderr,
        filesystem=mock_filesystem,
        env={'PWD': '/', 'HOME': '/home/test'},
        shell=None
    )
    process.cwd = '/'

    return process


@pytest.fixture
def sample_shell_script(tmp_path):
    """
    Provides a temporary shell script for testing.

    Returns:
        pathlib.Path: Path to test script
    """
    script = tmp_path / "test_script.sh"
    script.write_text("""#!/bin/bash
echo "Hello from script"
export TEST_VAR="test value"
for i in 1 2 3; do
    echo $i
done
""")
    return script


# ============================================================================
# Helper Functions
# ============================================================================

def assert_output_contains(process, expected: str):
    """
    Assert that process stdout contains expected string.

    Args:
        process: Process instance with captured output
        expected: Expected string in output
    """
    output = process.get_stdout()
    if isinstance(output, bytes):
        output = output.decode('utf-8', errors='replace')
    assert expected in output, f"Expected '{expected}' in output, got: {output}"


def assert_error_contains(process, expected: str):
    """
    Assert that process stderr contains expected string.

    Args:
        process: Process instance with captured output
        expected: Expected string in error output
    """
    error = process.get_stderr()
    if isinstance(error, bytes):
        error = error.decode('utf-8', errors='replace')
    assert expected in error, f"Expected '{expected}' in stderr, got: {error}"


def get_stdout(process) -> str:
    """Get stdout content as string."""
    output = process.get_stdout()
    if isinstance(output, bytes):
        return output.decode('utf-8', errors='replace')
    return output


def get_stderr(process) -> str:
    """Get stderr content as string."""
    error = process.get_stderr()
    if isinstance(error, bytes):
        return error.decode('utf-8', errors='replace')
    return error


# Make helper functions available as pytest helpers
pytest.assert_output_contains = assert_output_contains
pytest.assert_error_contains = assert_error_contains
pytest.get_stdout = get_stdout
pytest.get_stderr = get_stderr
