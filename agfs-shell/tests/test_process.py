"""
Tests for Process class.

Tests cover:
- Process initialization
- Stream handling (stdin, stdout, stderr)
- Exit code management
- Filesystem access through process
- Environment variable access
- Process execution
"""

import pytest
import io
from agfs_shell.process import Process
from agfs_shell.streams import InputStream, OutputStream


class TestProcessInitialization:
    """Test Process class initialization."""

    def test_process_creation_with_minimal_args(self):
        """Test creating process with minimal arguments."""
        process = Process(
            command='test',
            args=['arg1', 'arg2'],
        )

        assert process.command == 'test'
        assert process.args == ['arg1', 'arg2']
        assert process.stdin is not None
        assert process.stdout is not None
        assert process.stderr is not None

    def test_process_with_custom_streams(self, capture_output):
        """Test creating process with custom streams."""
        stdout, stderr = capture_output
        stdin = InputStream.from_bytes(b'input data')

        process = Process(
            command='test',
            args=[],
            stdin=stdin,
            stdout=stdout,
            stderr=stderr,
        )

        assert process.stdin == stdin
        assert process.stdout == stdout
        assert process.stderr == stderr

    def test_process_with_filesystem(self, mock_filesystem):
        """Test creating process with filesystem."""
        process = Process(
            command='test',
            args=[],
            filesystem=mock_filesystem,
        )

        assert process.filesystem == mock_filesystem

    def test_process_with_environment(self):
        """Test creating process with environment variables."""
        env = {'TEST_VAR': 'test_value', 'PATH': '/bin'}

        process = Process(
            command='test',
            args=[],
            env=env,
        )

        assert process.env == env
        assert process.env['TEST_VAR'] == 'test_value'

    def test_process_with_shell_reference(self, mock_shell):
        """Test creating process with shell reference."""
        process = Process(
            command='test',
            args=[],
            shell=mock_shell,
        )

        assert process.shell == mock_shell


class TestProcessStreams:
    """Test process stream handling."""

    def test_write_to_stdout(self, capture_output):
        """Test writing to process stdout."""
        stdout, stderr = capture_output

        process = Process(
            command='test',
            args=[],
            stdout=stdout,
            stderr=stderr,
        )

        process.stdout.write('Hello, World!')

        output = process.get_stdout()
        if isinstance(output, bytes):
            output = output.decode('utf-8')
        assert 'Hello, World!' in output

    def test_write_to_stderr(self, capture_output):
        """Test writing to process stderr."""
        stdout, stderr = capture_output

        process = Process(
            command='test',
            args=[],
            stdout=stdout,
            stderr=stderr,
        )

        process.stderr.write('Error message')

        error = process.get_stderr()
        if isinstance(error, bytes):
            error = error.decode('utf-8')
        assert 'Error message' in error

    def test_read_from_stdin(self):
        """Test reading from process stdin."""
        stdin = InputStream.from_bytes(b'test input\n')

        process = Process(
            command='test',
            args=[],
            stdin=stdin,
        )

        data = process.stdin.read()
        assert data == b'test input\n'

    def test_read_line_from_stdin(self):
        """Test reading lines from stdin."""
        stdin = InputStream.from_bytes(b'line1\nline2\nline3\n')

        process = Process(
            command='test',
            args=[],
            stdin=stdin,
        )

        line1 = process.stdin.readline()
        assert line1 == b'line1\n'

        line2 = process.stdin.readline()
        assert line2 == b'line2\n'

    def test_stream_iteration(self):
        """Test iterating over stdin lines."""
        stdin = InputStream.from_bytes(b'line1\nline2\nline3\n')

        process = Process(
            command='test',
            args=[],
            stdin=stdin,
        )

        lines = list(process.stdin)
        assert len(lines) == 3
        assert lines[0] == b'line1\n'
        assert lines[1] == b'line2\n'
        assert lines[2] == b'line3\n'


class TestProcessFilesystemAccess:
    """Test filesystem access through process."""

    def test_process_can_read_files(self, mock_filesystem):
        """Test process can read files through filesystem."""
        mock_filesystem.write_file('/test.txt', b'file content')

        process = Process(
            command='test',
            args=[],
            filesystem=mock_filesystem,
        )

        content = process.filesystem.read_file('/test.txt')
        assert content == b'file content'

    def test_process_can_write_files(self, mock_filesystem):
        """Test process can write files through filesystem."""
        process = Process(
            command='test',
            args=[],
            filesystem=mock_filesystem,
        )

        process.filesystem.write_file('/output.txt', b'test output')

        assert mock_filesystem.exists('/output.txt')
        content = mock_filesystem.read_file('/output.txt')
        assert content == b'test output'

    def test_process_can_list_directory(self, mock_filesystem):
        """Test process can list directories."""
        # The shared ``mock_filesystem`` fixture pre-creates ``/testdir`` with
        # two files, so ``create_directory('/testdir')`` would raise
        # ``FileExistsError`` and mask what this test is actually verifying.
        # Use a dedicated path so the test is self-contained and independent
        # of the fixture seed data.
        mock_filesystem.create_directory('/listdir')
        mock_filesystem.write_file('/listdir/file1.txt', b'content1')
        mock_filesystem.write_file('/listdir/file2.txt', b'content2')

        process = Process(
            command='test',
            args=[],
            filesystem=mock_filesystem,
        )

        entries = process.filesystem.list_directory('/listdir')
        assert len(entries) >= 2

        names = [e['name'] for e in entries]
        assert 'file1.txt' in names
        assert 'file2.txt' in names

    def test_process_handles_file_not_found(self, mock_filesystem):
        """Test process handles file not found errors."""
        process = Process(
            command='test',
            args=[],
            filesystem=mock_filesystem,
        )

        with pytest.raises(FileNotFoundError):
            process.filesystem.read_file('/nonexistent.txt')


class TestProcessEnvironment:
    """Test environment variable access through process."""

    def test_process_can_read_env_vars(self):
        """Test process can read environment variables."""
        env = {'TEST_VAR': 'value123', 'PATH': '/usr/bin'}

        process = Process(
            command='test',
            args=[],
            env=env,
        )

        assert process.env['TEST_VAR'] == 'value123'
        assert process.env['PATH'] == '/usr/bin'

    def test_process_can_modify_env_vars(self):
        """Test process can modify environment variables."""
        env = {'ORIGINAL': 'value'}

        process = Process(
            command='test',
            args=[],
            env=env,
        )

        process.env['NEW_VAR'] = 'new_value'
        process.env['ORIGINAL'] = 'modified'

        assert process.env['NEW_VAR'] == 'new_value'
        assert process.env['ORIGINAL'] == 'modified'

    def test_process_env_isolation(self):
        """Test process environment is independent."""
        env1 = {'VAR': 'value1'}
        env2 = {'VAR': 'value2'}

        process1 = Process(command='test1', args=[], env=env1)
        process2 = Process(command='test2', args=[], env=env2)

        assert process1.env['VAR'] == 'value1'
        assert process2.env['VAR'] == 'value2'

        # Modifying one shouldn't affect the other
        process1.env['VAR'] = 'modified'
        assert process2.env['VAR'] == 'value2'


class TestProcessExecution:
    """Test process execution mechanics."""

    def test_process_with_executor_function(self, capture_output):
        """Test process with custom executor."""
        stdout, stderr = capture_output

        def custom_executor():
            return 42

        process = Process(
            command='test',
            args=[],
            stdout=stdout,
            stderr=stderr,
            executor=custom_executor,
        )

        assert process.executor is not None
        result = process.executor()
        assert result == 42

    def test_process_stores_command_and_args(self):
        """Test process stores command and arguments."""
        process = Process(
            command='mycommand',
            args=['arg1', 'arg2', 'arg3'],
        )

        assert process.command == 'mycommand'
        assert process.args == ['arg1', 'arg2', 'arg3']
        assert len(process.args) == 3


class TestProcessStreamTypes:
    """Test different stream types and configurations."""

    def test_stdout_as_bytes_buffer(self):
        """Test stdout with bytes buffer."""
        buffer = io.BytesIO()
        stdout = OutputStream(buffer)

        process = Process(
            command='test',
            args=[],
            stdout=stdout,
        )

        process.stdout.write(b'binary data')
        assert buffer.getvalue() == b'binary data'

    def test_stderr_separate_from_stdout(self, capture_output):
        """Test stderr is separate from stdout."""
        stdout, stderr = capture_output

        process = Process(
            command='test',
            args=[],
            stdout=stdout,
            stderr=stderr,
        )

        process.stdout.write('normal output')
        process.stderr.write('error output')

        stdout_content = process.get_stdout()
        stderr_content = process.get_stderr()

        if isinstance(stdout_content, bytes):
            stdout_content = stdout_content.decode('utf-8')
        if isinstance(stderr_content, bytes):
            stderr_content = stderr_content.decode('utf-8')

        assert 'normal output' in stdout_content
        assert 'error output' in stderr_content
        assert 'error output' not in stdout_content

    def test_stdin_from_string(self):
        """Test stdin created from string."""
        stdin = InputStream.from_bytes(b'string input')

        process = Process(
            command='test',
            args=[],
            stdin=stdin,
        )

        content = process.stdin.read()
        assert content == b'string input'

    def test_stdin_from_file_like(self):
        """Test stdin from file-like object."""
        file_like = io.BytesIO(b'file content')
        stdin = InputStream(file_like)

        process = Process(
            command='test',
            args=[],
            stdin=stdin,
        )

        content = process.stdin.read()
        assert content == b'file content'


class TestProcessIntegration:
    """Integration tests for Process class."""

    def test_process_full_workflow(self, mock_filesystem, capture_output):
        """Test complete process workflow."""
        # Setup
        mock_filesystem.write_file('/input.txt', b'input data')
        stdout, stderr = capture_output
        stdin = InputStream.from_bytes(b'stdin data')

        # Create process
        process = Process(
            command='test_command',
            args=['--flag', 'value'],
            stdin=stdin,
            stdout=stdout,
            stderr=stderr,
            filesystem=mock_filesystem,
            env={'TEST': 'value'},
        )

        # Verify all components
        assert process.command == 'test_command'
        assert '--flag' in process.args
        assert process.env['TEST'] == 'value'
        assert process.filesystem.exists('/input.txt')

        # Read input
        content = process.filesystem.read_file('/input.txt')
        assert content == b'input data'

        # Write output
        process.stdout.write('success')
        process.stderr.write('no errors')

        # Verify output
        out = process.get_stdout()
        err = process.get_stderr()
        if isinstance(out, bytes):
            out = out.decode('utf-8')
        if isinstance(err, bytes):
            err = err.decode('utf-8')

        assert 'success' in out
        assert 'no errors' in err

    def test_multiple_processes_independent(self, mock_filesystem):
        """Test multiple processes are independent."""
        process1 = Process(
            command='cmd1',
            args=['arg1'],
            filesystem=mock_filesystem,
            env={'VAR': '1'},
        )

        process2 = Process(
            command='cmd2',
            args=['arg2'],
            filesystem=mock_filesystem,
            env={'VAR': '2'},
        )

        assert process1.command != process2.command
        assert process1.args != process2.args
        assert process1.env['VAR'] != process2.env['VAR']

        # But they share the same filesystem
        process1.filesystem.write_file('/shared.txt', b'data')
        assert process2.filesystem.exists('/shared.txt')


if __name__ == '__main__':
    pytest.main([__file__, '-v'])
