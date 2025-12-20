"""
UNALIAS command - remove command aliases.

Similar to bash's unalias command, removes defined aliases.
"""

from ..process import Process
from ..command_decorators import command
from . import register_command


@command()
@register_command('unalias')
def cmd_unalias(process: Process) -> int:
    """
    Remove command aliases

    Usage: unalias [-a] name [name ...]

    Options:
        -a      Remove all alias definitions

    Examples:
        unalias ll          # Remove the 'll' alias
        unalias ll la       # Remove multiple aliases
        unalias -a          # Remove all aliases
    """
    if not process.shell:
        process.stderr.write(b"unalias: shell context not available\n")
        return 1

    shell = process.shell

    if not process.args:
        process.stderr.write(b"unalias: usage: unalias [-a] name [name ...]\n")
        return 2

    # Check for -a flag
    if '-a' in process.args:
        shell.aliases.clear()
        return 0

    exit_code = 0
    for name in process.args:
        if name.startswith('-'):
            process.stderr.write(f"unalias: {name}: invalid option\n".encode('utf-8'))
            exit_code = 1
            continue

        if name in shell.aliases:
            del shell.aliases[name]
        else:
            process.stderr.write(f"unalias: {name}: not found\n".encode('utf-8'))
            exit_code = 1

    return exit_code
