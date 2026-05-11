"""Pipeline class for chaining processes together with true streaming"""

import threading
import queue
import io
from typing import List, Union
from .process import Process
from .streams import InputStream, OutputStream
from .control_flow import ControlFlowException


class StreamingPipeline:
    """
    True streaming pipeline implementation

    Processes run in parallel threads with streaming I/O between them.
    This prevents memory exhaustion on large data sets.
    """

    def __init__(self, processes: List[Process]):
        """
        Initialize a streaming pipeline

        Args:
            processes: List of Process objects to chain together
        """
        self.processes = processes
        self.exit_codes = []
        self.threads = []
        self.pipes = []  # Queue-based pipes between processes

    def execute(self) -> int:
        """
        Execute the entire pipeline with true streaming

        All processes run in parallel threads, connected by queues.
        Data flows through the pipeline in chunks without full buffering.

        Returns:
            Exit code of the last process
        """
        if not self.processes:
            return 0

        # Special case: single process (no piping needed)
        if len(self.processes) == 1:
            return self.processes[0].execute()

        # Create pipes (queues) between processes
        self.pipes = [queue.Queue(maxsize=10) for _ in range(len(self.processes) - 1)]
        self.exit_codes = [None] * len(self.processes)

        # Create wrapper streams that read from/write to queues
        for i, process in enumerate(self.processes):
            # Set up stdin: read from previous process's queue
            if i > 0:
                process.stdin = StreamingInputStream(self.pipes[i - 1])

            # Set up stdout: write to next process's queue
            if i < len(self.processes) - 1:
                process.stdout = StreamingOutputStream(self.pipes[i])

        # Start all processes in parallel threads
        for i, process in enumerate(self.processes):
            thread = threading.Thread(
                target=self._execute_process,
                args=(i, process),
                name=f"Process-{i}-{process.command}"
            )
            thread.start()
            self.threads.append(thread)

        # Wait for all processes to complete
        for thread in self.threads:
            thread.join()

        # Return exit code of last process
        return self.exit_codes[-1] if self.exit_codes else 0

    def _execute_process(self, index: int, process: Process):
        """
        Execute a single process in a thread

        Args:
            index: Process index in the pipeline
            process: Process object to execute
        """
        try:
            exit_code = process.execute()
            self.exit_codes[index] = exit_code
        except KeyboardInterrupt:
            # Let KeyboardInterrupt propagate for proper Ctrl-C handling
            raise
        except ControlFlowException:
            # Let control flow exceptions propagate
            raise
        except Exception as e:
            process.stderr.write(f"Pipeline error: {e}\n")
            self.exit_codes[index] = 1
        finally:
            # Signal EOF to next process by properly closing stdout
            # This ensures any buffered data is flushed before EOF
            if index < len(self.processes) - 1:
                if isinstance(process.stdout, StreamingOutputStream):
                    process.stdout.close()  # flush remaining buffer and send EOF
                else:
                    self.pipes[index].put(None)  # EOF marker


class StreamingInputStream(InputStream):
    """Input stream that reads from a queue in chunks.

    Reads pull whole chunks off a ``queue.Queue`` (sentinel ``None`` =
    EOF) and serve them out of a rolling ``bytearray`` buffer. The
    buffer is compacted in place when the consumed prefix grows large,
    so memory stays O(largest unread chunk) instead of growing with
    total stream size.

    Performance contract:

    * ``read(size)`` is O(consumed bytes) — copies the requested slice
      out of the buffer in one go rather than fan-in-via-many-Reads.
    * ``readline()`` is O(line length) — uses ``bytearray.find(b'\\n', start)``
      to scan the buffer in C, refilling from the queue when no newline
      is present. The previous implementation called ``read(1)`` per
      byte, which made each line cost O(N) Python-level iterations
      *and* O(N) ``io.BytesIO`` re-instantiations.

    Both methods share buffer state so a partial read followed by a
    readline reuses already-buffered bytes.
    """

    # When the consumed prefix grows beyond this many bytes, drop it
    # so the buffer's memory stays bounded for long streams. Below
    # this threshold we keep the prefix because the slicing cost
    # outweighs the wasted memory.
    _COMPACT_THRESHOLD = 4096

    def __init__(self, pipe: queue.Queue):
        super().__init__(None)
        self.pipe = pipe
        self._buf = bytearray()  # rolling buffer of pulled-but-unread bytes
        self._pos = 0            # how many bytes of self._buf the caller has consumed
        self._eof = False

    def _available(self) -> int:
        """Unread bytes currently in the rolling buffer."""
        return len(self._buf) - self._pos

    def _compact_if_needed(self) -> None:
        """Drop the consumed prefix of the buffer once it gets large.

        Without this, ``self._buf`` grows monotonically with total
        stream size even though we only ever read forward. Dropping
        in place via ``del self._buf[:self._pos]`` reuses the existing
        allocation.
        """
        if self._pos > self._COMPACT_THRESHOLD:
            del self._buf[:self._pos]
            self._pos = 0

    def _pull_chunk(self) -> bool:
        """Pull one chunk off the queue into the rolling buffer.

        Returns True if new bytes arrived, False on EOF. The chunk is
        appended; the caller is responsible for tracking how far it's
        consumed via ``self._pos``.
        """
        if self._eof:
            return False
        chunk = self.pipe.get()
        if chunk is None:  # EOF sentinel
            self._eof = True
            return False
        self._compact_if_needed()
        self._buf.extend(chunk)
        return True

    def read(self, size: int = -1) -> bytes:
        """Read up to ``size`` bytes, or everything until EOF if ``size < 0``."""
        if size < 0:
            # Drain the pipe to EOF and return everything buffered + pulled.
            while self._pull_chunk():
                pass
            data = bytes(self._buf[self._pos:])
            # Reset state so future reads after EOF return b''.
            self._buf.clear()
            self._pos = 0
            return data

        if size == 0:
            return b""

        # Refill until we have ``size`` bytes available or hit EOF.
        while self._available() < size:
            if not self._pull_chunk():
                break

        end = min(self._pos + size, len(self._buf))
        data = bytes(self._buf[self._pos:end])
        self._pos = end
        return data

    def readline(self) -> bytes:
        """Read up to and including the next newline, or to EOF.

        Uses ``bytearray.find`` (implemented in C) instead of the old
        ``read(1)``-per-byte loop. Big-O on long lines drops from
        quadratic to linear, and the constant factor drops by an order
        of magnitude.
        """
        while True:
            newline_idx = self._buf.find(b"\n", self._pos)
            if newline_idx != -1:
                end = newline_idx + 1
                data = bytes(self._buf[self._pos:end])
                self._pos = end
                return data
            if not self._pull_chunk():
                # EOF — return whatever's left (may be b'' if we hit a
                # clean stream end without a trailing newline).
                data = bytes(self._buf[self._pos:])
                self._buf.clear()
                self._pos = 0
                return data

    def readlines(self) -> list:
        """Read all lines until EOF as a list."""
        lines = []
        while True:
            line = self.readline()
            if not line:
                # readline returns b'' only when there's nothing
                # buffered AND the pipe is at EOF.
                break
            lines.append(line)
        return lines


class StreamingOutputStream(OutputStream):
    """Output stream that writes to a queue in chunks"""

    def __init__(self, pipe: queue.Queue, chunk_size: int = 8192):
        super().__init__(None)
        self.pipe = pipe
        self.chunk_size = chunk_size
        self._buffer = io.BytesIO()

    def write(self, data: Union[bytes, str]) -> int:
        """Write data to the queue-based pipe"""
        if isinstance(data, str):
            data = data.encode('utf-8')

        # Write to buffer
        self._buffer.write(data)

        # Flush chunks if buffer is large enough
        buffer_size = self._buffer.tell()
        if buffer_size >= self.chunk_size:
            self.flush()

        return len(data)

    def flush(self):
        """Flush buffered data to the queue"""
        self._buffer.seek(0)
        data = self._buffer.read()
        if data:
            self.pipe.put(data)
        self._buffer = io.BytesIO()

    def close(self):
        """Close the stream and flush remaining data"""
        self.flush()
        self.pipe.put(None)  # EOF marker


class Pipeline:
    """
    Hybrid pipeline implementation

    Uses streaming for pipelines that may have large data.
    Falls back to buffered execution for compatibility.
    """

    def __init__(self, processes: List[Process]):
        """
        Initialize a pipeline

        Args:
            processes: List of Process objects to chain together
        """
        self.processes = processes
        self.exit_codes = []
        self.use_streaming = len(processes) > 1  # Use streaming for multi-process pipelines

    def execute(self) -> int:
        """
        Execute the entire pipeline

        Automatically chooses between streaming and buffered execution.

        Returns:
            Exit code of the last process
        """
        if not self.processes:
            return 0

        # Use streaming pipeline for multi-process pipelines
        if self.use_streaming:
            streaming_pipeline = StreamingPipeline(self.processes)
            exit_code = streaming_pipeline.execute()
            self.exit_codes = streaming_pipeline.exit_codes
            return exit_code

        # Single process: execute directly (buffered)
        if not self.processes:
            return 0

        self.exit_codes = []

        # Execute processes in sequence, piping output to next input
        for i, process in enumerate(self.processes):
            # If this is not the first process, connect previous stdout to this stdin
            if i > 0:
                prev_process = self.processes[i - 1]
                prev_output = prev_process.get_stdout()
                process.stdin = InputStream.from_bytes(prev_output)

            # Execute the process
            exit_code = process.execute()
            self.exit_codes.append(exit_code)

        # Return exit code of last process
        return self.exit_codes[-1] if self.exit_codes else 0

    def get_stdout(self) -> bytes:
        """Get final stdout from the last process"""
        if not self.processes:
            return b''
        return self.processes[-1].get_stdout()

    def get_stderr(self) -> bytes:
        """Get combined stderr from all processes"""
        stderr_data = b''
        for process in self.processes:
            stderr_data += process.get_stderr()
        return stderr_data

    def get_exit_code(self) -> int:
        """Get exit code of the last process"""
        return self.exit_codes[-1] if self.exit_codes else 0

    def __repr__(self):
        pipeline_str = ' | '.join(str(p) for p in self.processes)
        return f"Pipeline({pipeline_str})"
