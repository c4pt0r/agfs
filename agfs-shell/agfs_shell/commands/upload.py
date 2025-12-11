"""
UPLOAD command - (auto-migrated from builtins.py)
"""

import os

from ..process import Process
from ..command_decorators import command
from . import register_command


@command()
@register_command('upload')
def cmd_upload(process: Process) -> int:
    """
    Upload a local file or directory to AGFS

    Usage: upload [-r] <local_path> <agfs_path>
    """
    # Parse arguments
    recursive = False
    args = process.args[:]

    if args and args[0] == '-r':
        recursive = True
        args = args[1:]

    if len(args) != 2:
        process.stderr.write("upload: usage: upload [-r] <local_path> <agfs_path>\n")
        return 1

    local_path = args[0]
    agfs_path = args[1]

    # Resolve agfs_path relative to current working directory
    if not agfs_path.startswith('/'):
        agfs_path = os.path.join(process.cwd, agfs_path)
        agfs_path = os.path.normpath(agfs_path)

    try:
        # Check if local path exists
        if not os.path.exists(local_path):
            process.stderr.write(f"upload: {local_path}: No such file or directory\n")
            return 1

        # Check if destination is a directory
        try:
            dest_info = process.filesystem.get_file_info(agfs_path)
            if dest_info.get('isDir', False):
                # Destination is a directory, append source filename
                source_basename = os.path.basename(local_path)
                agfs_path = os.path.join(agfs_path, source_basename)
                agfs_path = os.path.normpath(agfs_path)
        except Exception:
            # Destination doesn't exist, use as-is
            pass

        if os.path.isfile(local_path):
            # Upload single file
            return _upload_file(process, local_path, agfs_path)
        elif os.path.isdir(local_path):
            if not recursive:
                process.stderr.write(f"upload: {local_path}: Is a directory (use -r to upload recursively)\n")
                return 1
            # Upload directory recursively
            return _upload_dir(process, local_path, agfs_path)
        else:
            process.stderr.write(f"upload: {local_path}: Not a file or directory\n")
            return 1

    except Exception as e:
        error_msg = str(e)
        process.stderr.write(f"upload: {error_msg}\n")
        return 1


def _upload_file(process: Process, local_path: str, agfs_path: str, show_progress: bool = True) -> int:
    """Helper: Upload a single file to AGFS"""
    try:
        with open(local_path, 'rb') as f:
            data = f.read()
            process.filesystem.write_file(agfs_path, data, append=False)

        if show_progress:
            process.stdout.write(f"Uploaded {len(data)} bytes to {agfs_path}\n")
            process.stdout.flush()
        return 0

    except Exception as e:
        process.stderr.write(f"upload: {local_path}: {str(e)}\n")
        return 1


def _upload_dir(process: Process, local_path: str, agfs_path: str) -> int:
    """Helper: Upload a directory recursively to AGFS"""
    import stat as stat_module

    try:
        # Create target directory in AGFS if it doesn't exist
        try:
            info = process.filesystem.get_file_info(agfs_path)
            if not info.get('isDir', False):
                process.stderr.write(f"upload: {agfs_path}: Not a directory\n")
                return 1
        except Exception:
            # Directory doesn't exist, create it
            try:
                # Use mkdir command to create directory
                from pyagfs import AGFSClient
                process.filesystem.client.mkdir(agfs_path)
            except Exception as e:
                process.stderr.write(f"upload: cannot create directory {agfs_path}: {str(e)}\n")
                return 1

        # Walk through local directory
        for root, dirs, files in os.walk(local_path):
            # Calculate relative path
            rel_path = os.path.relpath(root, local_path)
            if rel_path == '.':
                current_agfs_dir = agfs_path
            else:
                current_agfs_dir = os.path.join(agfs_path, rel_path)
                current_agfs_dir = os.path.normpath(current_agfs_dir)

            # Create subdirectories in AGFS
            for dirname in dirs:
                dir_agfs_path = os.path.join(current_agfs_dir, dirname)
                dir_agfs_path = os.path.normpath(dir_agfs_path)
                try:
                    process.filesystem.client.mkdir(dir_agfs_path)
                except Exception:
                    # Directory might already exist, ignore
                    pass

            # Upload files
            for filename in files:
                local_file = os.path.join(root, filename)
                agfs_file = os.path.join(current_agfs_dir, filename)
                agfs_file = os.path.normpath(agfs_file)

                result = _upload_file(process, local_file, agfs_file)
                if result != 0:
                    return result

        return 0

    except Exception as e:
        process.stderr.write(f"upload: {str(e)}\n")
        return 1
