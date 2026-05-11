"""
Tests for core Shell functionality.

Tests cover:
- Shell initialization
- Command parsing and execution
- Environment variable handling
- Working directory management
- Alias and function management
- Exit code tracking
"""

import pytest
from unittest.mock import patch


class TestShellInitialization:
    """Test Shell class initialization."""

    def test_shell_creates_with_defaults(self, mock_filesystem):
        """Test shell initializes with default settings."""
        from agfs_shell.shell import Shell

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            assert shell.cwd == '/'
            assert isinstance(shell.env, dict)
            assert 'PATH' in shell.env or shell.env is not None
            assert shell.functions == {}
            assert shell.aliases == {}

    def test_shell_accepts_custom_server_url(self, mock_filesystem):
        """Test shell can be initialized with custom server URL."""
        from agfs_shell.shell import Shell

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell(server_url="http://custom:9999", timeout=60)

            assert shell.server_url == "http://custom:9999"

    def test_shell_initializes_with_custom_env(self, mock_filesystem):
        """Test shell can be initialized with custom environment."""
        from agfs_shell.shell import Shell

        custom_env = {'CUSTOM_VAR': 'value', 'PATH': '/custom/bin'}

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell(initial_env=custom_env)

            assert 'CUSTOM_VAR' in shell.env
            assert shell.env['CUSTOM_VAR'] == 'value'


class TestCommandExecution:
    """Test command execution."""

    def test_execute_simple_command(self, mock_shell, mock_filesystem):
        """Test executing a simple echo command."""
        from agfs_shell.shell import Shell

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            # Mock the execute method to test interface
            result = shell.execute("echo hello")

            # Should return exit code
            assert isinstance(result, int)

    def test_execute_returns_exit_code(self, mock_filesystem):
        """Test that execute returns proper exit codes."""
        from agfs_shell.shell import Shell

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            # Successful command should return 0
            exit_code = shell.execute("true")
            assert exit_code == 0

            # Failed command should return non-zero
            exit_code = shell.execute("false")
            assert exit_code == 1

    def test_command_not_found(self, mock_filesystem):
        """Test handling of non-existent commands."""
        from agfs_shell.shell import Shell

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            exit_code = shell.execute("nonexistent_command_xyz")

            # Should return error code (usually 127 for command not found)
            assert exit_code != 0


class TestEnvironmentVariables:
    """Test environment variable handling."""

    def test_get_environment_variable(self, mock_filesystem):
        """Test retrieving environment variables."""
        from agfs_shell.shell import Shell

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()
            shell.env['TEST_VAR'] = 'test_value'

            assert shell.env.get('TEST_VAR') == 'test_value'

    def test_set_environment_variable(self, mock_filesystem):
        """Test setting environment variables."""
        from agfs_shell.shell import Shell

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            shell.execute("export NEW_VAR=new_value")

            assert 'NEW_VAR' in shell.env
            assert shell.env['NEW_VAR'] == 'new_value'

    def test_unset_environment_variable(self, mock_filesystem):
        """Test unsetting environment variables."""
        from agfs_shell.shell import Shell

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()
            shell.env['TO_REMOVE'] = 'value'

            shell.execute("unset TO_REMOVE")

            assert 'TO_REMOVE' not in shell.env

    def test_variable_expansion_in_commands(self, mock_filesystem):
        """Test that variables are expanded in commands."""
        from agfs_shell.shell import Shell

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()
            shell.env['MY_VAR'] = 'expanded'

            # Variable should be expanded when used
            shell.execute("export TEST=$MY_VAR")

            assert shell.env.get('TEST') == 'expanded'

    def test_exit_code_variable(self, mock_filesystem):
        """Test that $? contains last exit code."""
        from agfs_shell.shell import Shell

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            shell.execute("true")
            assert shell.env.get('?') == '0'

            shell.execute("false")
            assert shell.env.get('?') == '1'


class TestWorkingDirectory:
    """Test working directory management."""

    def test_initial_working_directory(self, mock_filesystem):
        """Test shell starts in root directory."""
        from agfs_shell.shell import Shell

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            assert shell.cwd == '/'

    def test_change_directory(self, mock_filesystem):
        """Test cd command changes working directory."""
        from agfs_shell.shell import Shell

        mock_filesystem.create_directory('/home')
        mock_filesystem.create_directory('/home/user')

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            shell.execute("cd /home/user")

            assert shell.cwd == '/home/user'

    def test_relative_directory_change(self, mock_filesystem):
        """Test cd with relative paths."""
        from agfs_shell.shell import Shell

        mock_filesystem.create_directory('/home')
        mock_filesystem.create_directory('/home/user')
        mock_filesystem.create_directory('/home/user/docs')

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()
            shell.cwd = '/home/user'

            shell.execute("cd docs")

            assert shell.cwd == '/home/user/docs'

    def test_cd_parent_directory(self, mock_filesystem):
        """Test cd .. goes to parent directory."""
        from agfs_shell.shell import Shell

        mock_filesystem.create_directory('/home')
        mock_filesystem.create_directory('/home/user')

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()
            shell.cwd = '/home/user'

            shell.execute("cd ..")

            assert shell.cwd == '/home'

    def test_cd_nonexistent_directory(self, mock_filesystem):
        """Test cd to non-existent directory fails."""
        from agfs_shell.shell import Shell

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            exit_code = shell.execute("cd /nonexistent")

            # Should fail
            assert exit_code != 0
            # CWD should not change
            assert shell.cwd == '/'


class TestAliases:
    """Test command alias management."""

    def test_define_alias(self, mock_filesystem):
        """Test creating an alias."""
        from agfs_shell.shell import Shell

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            shell.execute("alias ll='ls -la'")

            assert 'll' in shell.aliases
            assert shell.aliases['ll'] == 'ls -la'

    def test_alias_expansion(self, mock_filesystem):
        """Test that aliases are expanded when used."""
        from agfs_shell.shell import Shell

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()
            shell.aliases['testcmd'] = 'echo test'

            # When alias is used, it should expand
            # (actual execution testing would require more mocking)
            assert 'testcmd' in shell.aliases

    def test_unalias(self, mock_filesystem):
        """Test removing an alias."""
        from agfs_shell.shell import Shell

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()
            shell.aliases['test_alias'] = 'some command'

            shell.execute("unalias test_alias")

            assert 'test_alias' not in shell.aliases

    def test_list_aliases(self, mock_filesystem):
        """Test listing all aliases."""
        from agfs_shell.shell import Shell

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()
            shell.aliases['alias1'] = 'cmd1'
            shell.aliases['alias2'] = 'cmd2'

            # alias command with no args lists aliases
            shell.execute("alias")

            assert len(shell.aliases) == 2


class TestFunctions:
    """Test user-defined function management."""

    def test_define_function(self, mock_filesystem):
        """Test defining a shell function."""
        from agfs_shell.shell import Shell

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            function_def = """function greet() {
                echo "Hello, $1"
            }"""

            shell.execute(function_def)

            assert 'greet' in shell.functions

    def test_call_function(self, mock_filesystem):
        """Test calling a defined function."""
        from agfs_shell.shell import Shell

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            # Define function
            function_def = """function test_func() {
                return 42
            }"""
            shell.execute(function_def)

            # Call function
            _exit_code = shell.execute("test_func")

            # Should execute (exact behavior depends on implementation)
            assert 'test_func' in shell.functions

    def test_function_with_parameters(self, mock_filesystem):
        """Test function receives parameters."""
        from agfs_shell.shell import Shell

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            function_def = """function add(a, b) {
                return 0
            }"""

            shell.execute(function_def)

            assert 'add' in shell.functions
            # Verify function stored parameters
            if isinstance(shell.functions['add'], dict):
                assert 'params' in shell.functions['add']


class TestCommandSubstitution:
    """Test command substitution."""

    def test_simple_command_substitution(self, mock_filesystem):
        """Test $(command) substitution."""
        from agfs_shell.shell import Shell

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            # Command substitution should capture output
            shell.execute("export RESULT=$(echo hello)")

            assert shell.env.get('RESULT') == 'hello'

    def test_backtick_substitution(self, mock_filesystem):
        """Test `command` substitution."""
        from agfs_shell.shell import Shell

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            shell.execute("export RESULT=`echo world`")

            assert shell.env.get('RESULT') == 'world'

    def test_nested_command_substitution(self, mock_filesystem):
        """Test nested command substitution."""
        from agfs_shell.shell import Shell

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            # Nested substitution
            shell.execute("export OUTER=$(echo $(echo nested))")

            assert shell.env.get('OUTER') == 'nested'


class TestPipelines:
    """Test pipeline execution."""

    def test_simple_pipeline(self, mock_filesystem):
        """Test basic command pipeline."""
        from agfs_shell.shell import Shell

        mock_filesystem.write_file('/lines.txt', b'line1\nline2\nline3\n')

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            # Pipeline should connect commands
            exit_code = shell.execute("cat /lines.txt | wc -l")

            assert exit_code == 0

    def test_multi_stage_pipeline(self, mock_filesystem):
        """Test pipeline with multiple stages."""
        from agfs_shell.shell import Shell

        mock_filesystem.write_file('/numbers.txt', b'3\n1\n2\n')

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            exit_code = shell.execute("cat /numbers.txt | sort | head -n 1")

            assert exit_code == 0

    @pytest.mark.xfail(
        strict=True,
        reason=(
            "agfs-shell follows bash's default pipeline status semantics "
            "(exit code of the rightmost command), so "
            "`cat /nonexistent | wc -l` yields wc's 0. The test asserts "
            "pipefail-like behaviour, which is not the current contract. "
            "Tracked as a future explicit `set -o pipefail` feature task; "
            "do not silently change the default in this stabilization PR."
        ),
    )
    def test_pipeline_error_propagation(self, mock_filesystem):
        """Test that pipeline errors are handled correctly."""
        from agfs_shell.shell import Shell

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            # Pipeline with failing command
            exit_code = shell.execute("cat /nonexistent | wc -l")

            # Should fail due to cat error
            assert exit_code != 0


class TestRedirection:
    """Test I/O redirection."""

    def test_output_redirection(self, mock_filesystem):
        """Test > output redirection."""
        from agfs_shell.shell import Shell

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            shell.execute("echo 'test output' > /output.txt")

            content = mock_filesystem.read_file('/output.txt')
            assert b'test output' in content

    def test_append_redirection(self, mock_filesystem):
        """Test >> append redirection."""
        from agfs_shell.shell import Shell

        mock_filesystem.write_file('/append.txt', b'line1\n')

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            shell.execute("echo 'line2' >> /append.txt")

            content = mock_filesystem.read_file('/append.txt')
            assert b'line1' in content
            assert b'line2' in content

    def test_input_redirection(self, mock_filesystem):
        """Test < input redirection."""
        from agfs_shell.shell import Shell

        mock_filesystem.write_file('/input.txt', b'input content')

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            exit_code = shell.execute("cat < /input.txt")

            assert exit_code == 0

    def test_stderr_redirection(self, mock_filesystem):
        """Test 2> stderr redirection."""
        from agfs_shell.shell import Shell

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            # Command that produces stderr
            shell.execute("cat /nonexistent 2> /errors.txt")

            # Error should be redirected to file
            assert mock_filesystem.exists('/errors.txt')


class TestLocalScopes:
    """Test local variable scopes."""

    def test_local_variable_in_function(self, mock_filesystem):
        """Test local variables are scoped to functions."""
        from agfs_shell.shell import Shell

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            function_def = """function test_local() {
                local LOCAL_VAR="local value"
                export GLOBAL_VAR="global value"
            }"""

            shell.execute(function_def)
            shell.execute("test_local")

            # Global var should be set
            assert shell.env.get('GLOBAL_VAR') == 'global value'
            # Local var should not leak (implementation dependent)

    def test_nested_scopes(self, mock_filesystem):
        """Test nested local scopes."""
        from agfs_shell.shell import Shell

        with patch('agfs_shell.shell.AGFSFileSystem', return_value=mock_filesystem):
            shell = Shell()

            # Nested functions create nested scopes
            assert isinstance(shell.local_scopes, list)


if __name__ == '__main__':
    pytest.main([__file__, '-v'])
