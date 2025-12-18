"""
TRUNCATE command - truncate file to specified size.
"""

from ..process import Process
from ..command_decorators import command
from . import register_command


@command(needs_path_resolution=True)
@register_command('truncate')
def cmd_truncate(process: Process) -> int:
    """
    Truncate file to specified size

    Usage: truncate -s SIZE FILE...
           truncate --size=SIZE FILE...

    Options:
      -s, --size=SIZE    Set file size to SIZE bytes
      -h, --help         Display this help and exit

    SIZE may be an integer number of bytes.
    If SIZE is less than current file size, extra data is lost.
    If SIZE is greater, file is extended with null bytes.

    Examples:
      truncate -s 0 file.txt        # Truncate file to zero bytes (empty file)
      truncate -s 1024 file.txt     # Truncate/extend file to 1024 bytes
      truncate --size=100 f1 f2     # Truncate multiple files to 100 bytes
    """
    if not process.filesystem:
        process.stderr.write("truncate: filesystem not available\n")
        return 1

    # Parse arguments
    size = None
    files = []
    i = 0
    args = process.args

    while i < len(args):
        arg = args[i]
        
        if arg in ('-h', '--help'):
            process.stdout.write(cmd_truncate.__doc__ + "\n")
            return 0
        elif arg == '-s':
            # -s SIZE format
            if i + 1 >= len(args):
                process.stderr.write("truncate: option requires an argument -- 's'\n")
                return 1
            try:
                size = int(args[i + 1])
                if size < 0:
                    process.stderr.write("truncate: invalid size: negative size not allowed\n")
                    return 1
            except ValueError:
                process.stderr.write(f"truncate: invalid size: '{args[i + 1]}'\n")
                return 1
            i += 2
        elif arg.startswith('--size='):
            # --size=SIZE format
            size_str = arg.split('=', 1)[1]
            try:
                size = int(size_str)
                if size < 0:
                    process.stderr.write("truncate: invalid size: negative size not allowed\n")
                    return 1
            except ValueError:
                process.stderr.write(f"truncate: invalid size: '{size_str}'\n")
                return 1
            i += 1
        elif arg.startswith('-'):
            process.stderr.write(f"truncate: invalid option -- '{arg}'\n")
            process.stderr.write("Try 'truncate --help' for more information.\n")
            return 1
        else:
            # This is a file argument
            files.append(arg)
            i += 1

    # Validate arguments
    if size is None:
        process.stderr.write("truncate: you must specify a size\n")
        process.stderr.write("Try 'truncate --help' for more information.\n")
        return 1

    if not files:
        process.stderr.write("truncate: missing file operand\n")
        process.stderr.write("Try 'truncate --help' for more information.\n")
        return 1

    # Truncate each file
    exit_code = 0
    for path in files:
        try:
            process.filesystem.client.truncate(path, size)
        except Exception as e:
            error_msg = str(e)
            process.stderr.write(f"truncate: {path}: {error_msg}\n")
            exit_code = 1

    return exit_code

