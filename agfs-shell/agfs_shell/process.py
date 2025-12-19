"""Process class for command execution in pipelines"""

from typing import List, Optional, Callable, TYPE_CHECKING

if TYPE_CHECKING:
    from .filesystem import AGFSFileSystem
    from .shell import Shell

from .streams import InputStream, OutputStream, ErrorStream
from .control_flow import ControlFlowException


class Process:
    """Represents a single process/command in a pipeline"""

    def __init__(
        self,
        command: str,
        args: List[str],
        stdin: Optional[InputStream] = None,
        stdout: Optional[OutputStream] = None,
        stderr: Optional[ErrorStream] = None,
        executor: Optional[Callable] = None,
        filesystem: Optional['AGFSFileSystem'] = None,
        env: Optional[dict] = None,
        shell: Optional['Shell'] = None
    ):
        """
        Initialize a process

        Args:
            command: Command name
            args: Command arguments
            stdin: Input stream
            stdout: Output stream
            stderr: Error stream
            executor: Callable that executes the command
            filesystem: AGFS file system instance for file operations
            env: Environment variables dictionary
            shell: Shell instance for commands that need shell access (e.g., source)
        """
        self.command = command
        self.args = args
        self.stdin = stdin or InputStream.from_bytes(b'')
        self.stdout = stdout or OutputStream.to_buffer()
        self.stderr = stderr or ErrorStream.to_buffer()
        self.executor = executor
        self.filesystem = filesystem
        self.env = env or {}
        self.shell = shell
        self.exit_code = 0

    def execute(self) -> int:
        """
        Execute the process

        Returns:
            Exit code (0 for success, non-zero for error)
        """
        if self.executor is None:
            self.stderr.write(f"Error: No such command '{self.command}'\n")
            self.exit_code = 127
            return self.exit_code

        try:
            # Execute the command
            self.exit_code = self.executor(self)
        except KeyboardInterrupt:
            # Let KeyboardInterrupt propagate for proper Ctrl-C handling
            raise
        except ControlFlowException:
            # Let control flow exceptions (break, continue, return) propagate
            raise
        except Exception as e:
            self.stderr.write(f"Error executing '{self.command}': {str(e)}\n")
            self.exit_code = 1

        # Flush all streams
        self.stdout.flush()
        self.stderr.flush()

        return self.exit_code

    def get_stdout(self) -> bytes:
        """Get stdout contents"""
        return self.stdout.get_value()

    def get_stderr(self) -> bytes:
        """Get stderr contents"""
        return self.stderr.get_value()

    def __repr__(self):
        args_str = ' '.join(self.args) if self.args else ''
        return f"Process({self.command} {args_str})"
