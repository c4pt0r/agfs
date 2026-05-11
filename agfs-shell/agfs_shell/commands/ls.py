"""
LS command - list directory contents.
"""

import os

from pyagfs.exceptions import AGFSClientError

from ..process import Process
from ..command_decorators import command
from ..utils.formatters import mode_to_rwx, human_readable_size
from . import register_command


@command(needs_path_resolution=True)
@register_command('ls')
def cmd_ls(process: Process) -> int:
    """
    List directory contents

    Usage: ls [-l] [-h] [path...]

    Options:
        -l    Use long listing format
        -h    Print human-readable sizes (e.g., 1K, 234M, 2G)
    """
    # Parse arguments
    long_format = False
    human_readable_flag = False
    paths = []

    for arg in process.args:
        if arg.startswith('-') and arg != '-':
            # Handle combined flags like -lh
            if 'l' in arg:
                long_format = True
            if 'h' in arg:
                human_readable_flag = True
        else:
            paths.append(arg)

    # Default to current working directory if no paths specified
    if not paths:
        paths = [process.context.cwd]

    if not process.context.filesystem:
        process.stderr.write("ls: filesystem not available\n")
        return 1

    # Helper function to format file info
    def format_file_info(file_info, display_name=None, full_path=None):
        """Format a single file info dict for output"""
        name = display_name if display_name else file_info.get('name', '')
        is_dir = file_info.get('isDir', False) or file_info.get('type') == 'directory'
        size = file_info.get('size', 0)

        # Check if this is a symlink
        meta = file_info.get('meta', {})
        is_symlink = meta.get('Type') == 'symlink'

        # Get symlink target if this is a symlink. ``readlink`` raises
        # ``AGFSClientError`` for the documented "not a symlink /
        # missing / unreadable" cases — those are the only failure
        # modes where falling back to a plain-file display is correct.
        # Any other exception type (programmer error, transport bug)
        # bubbles up so the user actually hears about it.
        symlink_target = None
        if is_symlink and full_path:
            try:
                symlink_target = process.context.filesystem.readlink(full_path)
            except AGFSClientError:
                # readlink failed — fall back to plain-file display.
                pass

        if long_format:
            # Long format output similar to ls -l
            if is_symlink:
                file_type = 'l'  # 'l' for symlink in ls -l
            elif is_dir:
                file_type = 'd'
            else:
                file_type = '-'

            # Get mode/permissions
            mode_str = file_info.get('mode', '')
            if mode_str and isinstance(mode_str, str) and len(mode_str) >= 9:
                # Already in rwxr-xr-x format
                perms = mode_str[:9]
            elif mode_str and isinstance(mode_str, int):
                # Convert octal mode to rwx format
                perms = mode_to_rwx(mode_str)
            else:
                # Default permissions
                if is_symlink:
                    perms = 'rwxrwxrwx'  # Symlinks typically show 777
                elif is_dir:
                    perms = 'rwxr-xr-x'
                else:
                    perms = 'rw-r--r--'

            # Get modification time
            mtime = file_info.get('modTime', file_info.get('mtime', ''))
            if mtime:
                # Format timestamp (YYYY-MM-DD HH:MM:SS)
                if 'T' in mtime:
                    # ISO format: 2025-11-18T22:00:25Z
                    mtime = mtime.replace('T', ' ').replace('Z', '').split('.')[0]
                elif len(mtime) > 19:
                    # Truncate to 19 chars if too long
                    mtime = mtime[:19]
            else:
                mtime = '0000-00-00 00:00:00'

            # Format: permissions size date time name
            if is_symlink and symlink_target:
                # Cyan color for symlinks with arrow
                colored_name = f"\033[1;36m{name}\033[0m -> \033[1;36m{symlink_target}\033[0m"
            elif is_dir:
                # Blue color for directories
                colored_name = f"\033[1;34m{name}/\033[0m"
            else:
                colored_name = name

            # Format size based on human_readable flag
            if human_readable_flag:
                size_str = f"{human_readable_size(size):>8}"
            else:
                size_str = f"{size:>8}"

            return f"{file_type}{perms} {size_str} {mtime} {colored_name}\n"
        else:
            # Simple formatting
            if is_symlink and symlink_target:
                # Cyan color for symlinks with arrow
                return f"\033[1;36m{name}\033[0m -> \033[1;36m{symlink_target}\033[0m\n"
            elif is_dir:
                # Blue color for directories
                return f"\033[1;34m{name}/\033[0m\n"
            else:
                return f"{name}\n"

    exit_code = 0

    try:
        # Process each path argument
        for path in paths:
            try:
                # First, get info about the path to determine if it's a file or directory
                path_info = process.context.filesystem.get_file_info(path)
                is_directory = path_info.get('isDir', False) or path_info.get('type') == 'directory'

                if is_directory:
                    # It's a directory - list its contents
                    files = process.context.filesystem.list_directory(path)

                    # Show directory name if multiple paths
                    if len(paths) > 1:
                        process.stdout.write(f"{path}:\n".encode('utf-8'))

                    for file_info in files:
                        # Construct full path for the file
                        file_name = file_info.get('name', '')
                        if path.endswith('/'):
                            full_file_path = path + file_name
                        else:
                            full_file_path = path + '/' + file_name

                        output = format_file_info(file_info, full_path=full_file_path)
                        process.stdout.write(output.encode('utf-8'))

                    # Add blank line between directories if multiple paths
                    if len(paths) > 1:
                        process.stdout.write(b"\n")
                else:
                    # It's a file - display info about the file itself
                    basename = os.path.basename(path)
                    output = format_file_info(path_info, display_name=basename, full_path=path)
                    process.stdout.write(output.encode('utf-8'))

            except Exception as e:
                error_msg = str(e)
                if "No such file or directory" in error_msg or "not found" in error_msg.lower():
                    process.stderr.write(f"ls: {path}: No such file or directory\n")
                else:
                    process.stderr.write(f"ls: {path}: {error_msg}\n")
                exit_code = 1

        return exit_code
    except Exception as e:
        error_msg = str(e)
        process.stderr.write(f"ls: {error_msg}\n")
        return 1
