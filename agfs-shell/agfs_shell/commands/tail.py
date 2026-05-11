"""
TAIL command - output the last part of files.
"""

import time

from pyagfs.exceptions import AGFSClientError

from ..process import Process
from ..command_decorators import command
from . import register_command


@command(needs_path_resolution=True, supports_streaming=True)
@register_command('tail')
def cmd_tail(process: Process) -> int:
    """
    Output the last part of files

    Usage: tail [-n count] [-f] [-F] [file...]

    Options:
        -n count    Output the last count lines (default: 10)
        -f          Follow mode: show last n lines, then continuously follow
        -F          Stream mode: for streamfs/streamrotatefs only
                    Continuously reads from the stream without loading history
                    Ideal for infinite streams like /streamfs/* or /streamrotate/*
    """
    n = 10  # default
    follow = False
    stream_only = False  # -F flag: skip reading history
    files = []

    # Parse flags
    args = process.args[:]
    i = 0
    while i < len(args):
        if args[i] == '-n' and i + 1 < len(args):
            try:
                n = int(args[i + 1])
                i += 2
                continue
            except ValueError:
                process.stderr.write(f"tail: invalid number: {args[i + 1]}\n")
                return 1
        elif args[i] == '-f':
            follow = True
            i += 1
        elif args[i] == '-F':
            follow = True
            stream_only = True
            i += 1
        else:
            # This is a file argument
            files.append(args[i])
            i += 1

    # Handle stdin or files
    if not files:
        # Read from stdin
        lines = process.stdin.readlines()
        for line in lines[-n:]:
            process.stdout.write(line)

        if follow:
            process.stderr.write(b"tail: warning: following stdin is not supported\n")

        return 0

    # Read from files
    if not follow:
        # Normal tail mode - read last n lines from each file
        for filename in files:
            try:
                if not process.context.filesystem:
                    process.stderr.write(b"tail: filesystem not available\n")
                    return 1

                # Use streaming mode to read entire file
                stream = process.context.filesystem.read_file(filename, stream=True)
                chunks = []
                for chunk in stream:
                    if chunk:
                        chunks.append(chunk)
                content = b''.join(chunks)
                lines = content.decode('utf-8', errors='replace').splitlines(keepends=True)
                for line in lines[-n:]:
                    process.stdout.write(line)
            except Exception as e:
                process.stderr.write(f"tail: {filename}: {str(e)}\n")
                return 1
    else:
        # Follow mode - continuously read new content
        if len(files) > 1:
            process.stderr.write(b"tail: warning: following multiple files not yet supported, using first file\n")

        filename = files[0]

        try:
            if process.context.filesystem:
                if stream_only:
                    # -F mode: Stream-only mode for filesystems that support streaming
                    # This mode uses continuous streaming read without loading history
                    process.stderr.write(b"==> Continuously reading from stream <==\n")
                    process.stdout.flush()

                    # Use continuous streaming read
                    try:
                        stream = process.context.filesystem.read_file(filename, stream=True)
                        for chunk in stream:
                            if chunk:
                                process.stdout.write(chunk)
                                process.stdout.flush()
                    except KeyboardInterrupt:
                        # Re-raise to allow proper signal propagation in script mode
                        raise
                    except Exception as e:
                        error_msg = str(e)
                        # Check if it's a streaming-related error
                        if "stream mode" in error_msg.lower() or "use stream" in error_msg.lower():
                            process.stderr.write(f"tail: {filename}: {error_msg}\n".encode())
                            process.stderr.write(b"      Note: -F requires a filesystem that supports streaming\n")
                        else:
                            process.stderr.write(f"tail: {filename}: {error_msg}\n".encode())
                        return 1
                else:
                    # -f mode: Traditional follow mode
                    # First, output the last n lines
                    stream = process.context.filesystem.read_file(filename, stream=True)
                    chunks = []
                    for chunk in stream:
                        if chunk:
                            chunks.append(chunk)
                    content = b''.join(chunks)
                    lines = content.decode('utf-8', errors='replace').splitlines(keepends=True)
                    for line in lines[-n:]:
                        process.stdout.write(line)
                    process.stdout.flush()

                    # Get current file size
                    file_info = process.context.filesystem.get_file_info(filename)
                    current_size = file_info.get('size', 0)

                    # Now continuously poll for new content
                    try:
                        while True:
                            time.sleep(0.1)  # Poll every 100ms

                            # Check file size. Only "file disappeared
                            # / unreadable" (AGFSClientError) is a
                            # legitimate continue case here. Anything
                            # else (programmer error, broken
                            # filesystem object) must bubble — otherwise
                            # `tail -f` becomes an infinite no-op on
                            # the buggy condition.
                            try:
                                file_info = process.context.filesystem.get_file_info(filename)
                                new_size = file_info.get('size', 0)
                            except AGFSClientError:
                                # File might not exist yet, keep waiting
                                continue

                            if new_size > current_size:
                                # Read new content from offset using streaming
                                stream = process.context.filesystem.read_file(
                                    filename,
                                    offset=current_size,
                                    size=new_size - current_size,
                                    stream=True
                                )
                                for chunk in stream:
                                    if chunk:
                                        process.stdout.write(chunk)
                                process.stdout.flush()
                                current_size = new_size
                    except KeyboardInterrupt:
                        # Re-raise to allow proper signal propagation in script mode
                        raise
            else:
                # No filesystem - should not happen in normal usage
                process.stderr.write(b"tail: filesystem not available\n")
                return 1

        except Exception as e:
            process.stderr.write(f"tail: {filename}: {str(e)}\n")
            return 1

    return 0
