"""
SOURCE command - execute commands from a file in the current shell environment.

This is equivalent to Bash's 'source' or '.' command.
Variables and functions defined in the sourced file persist in the current shell.
"""

import os
from ..process import Process
from ..command_decorators import command
from . import register_command


@command()
@register_command('source', '.')
def cmd_source(process: Process) -> int:
    """
    Execute commands from a file in the current shell environment.

    Usage: source FILENAME [ARGUMENTS...]
           . FILENAME [ARGUMENTS...]

    The sourced file is executed in the current shell environment.
    Variables and functions defined in the file persist after execution.

    Arguments passed to source are available as $1, $2, etc. in the sourced file.
    The original positional parameters are restored after execution.

    Examples:
        source /etc/profile.as
        source lib.sh
        . ~/.bashrc
        source config.sh arg1 arg2
    """
    if not process.args:
        process.stderr.write("source: usage: source FILENAME [ARGUMENTS...]\n")
        return 2

    if not process.shell:
        process.stderr.write("source: shell context not available\n")
        return 1

    shell = process.shell
    filename = process.args[0]
    script_args = process.args[1:] if len(process.args) > 1 else None

    # Resolve the file path
    if filename.startswith('/'):
        file_path = filename
    else:
        # Relative path - resolve from current directory
        file_path = os.path.join(shell.cwd, filename)
        file_path = os.path.normpath(file_path)

    # Use shell.execute_script directly
    result = shell.execute_script(file_path, script_args=script_args, silent=True)

    if result is None:
        process.stderr.write(f"source: {filename}: No such file or directory\n")
        return 1

    return result
