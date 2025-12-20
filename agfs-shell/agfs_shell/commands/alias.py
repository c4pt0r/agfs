"""
ALIAS command - define or display command aliases.

Similar to bash's alias command, allows users to create shortcuts for commands.
"""

from ..process import Process
from ..command_decorators import command
from . import register_command


@command()
@register_command('alias')
def cmd_alias(process: Process) -> int:
    """
    Define or display command aliases

    Usage: alias [name[=value] ...]

    Without arguments, prints all defined aliases.
    With a name argument, prints the alias definition for that name.
    With name=value, creates an alias where 'name' expands to 'value'.

    Examples:
        alias                       # List all aliases
        alias ll                    # Show alias for 'll'
        alias ll='ls -l'            # Create alias 'll' for 'ls -l'
        alias la='ls -la'           # Create alias 'la' for 'ls -la'
        alias ..='cd ..'            # Create alias '..' for 'cd ..'

    Note: Aliases are expanded before command execution. To prevent alias
    expansion, quote the command or use a backslash: \\command
    """
    if not process.shell:
        process.stderr.write(b"alias: shell context not available\n")
        return 1

    shell = process.shell

    if not process.args:
        # No args: list all aliases
        if not shell.aliases:
            return 0
        for name, value in sorted(shell.aliases.items()):
            process.stdout.write(f"alias {name}='{value}'\n".encode('utf-8'))
        return 0

    exit_code = 0
    for arg in process.args:
        if '=' in arg:
            # Define alias: name=value or name='value' or name="value"
            eq_pos = arg.index('=')
            name = arg[:eq_pos]
            value = arg[eq_pos + 1:]

            # Validate alias name
            if not name or not _is_valid_alias_name(name):
                process.stderr.write(f"alias: `{name}': invalid alias name\n".encode('utf-8'))
                exit_code = 1
                continue

            # Remove surrounding quotes if present
            if len(value) >= 2:
                if (value[0] == '"' and value[-1] == '"') or \
                   (value[0] == "'" and value[-1] == "'"):
                    value = value[1:-1]

            shell.aliases[name] = value
        else:
            # Show alias for a specific name
            name = arg
            if name in shell.aliases:
                process.stdout.write(f"alias {name}='{shell.aliases[name]}'\n".encode('utf-8'))
            else:
                process.stderr.write(f"alias: {name}: not found\n".encode('utf-8'))
                exit_code = 1

    return exit_code


def _is_valid_alias_name(name: str) -> bool:
    """
    Check if a string is a valid alias name.
    Alias names can contain letters, digits, underscores, and hyphens,
    but must start with a letter or underscore.
    """
    if not name:
        return False

    # First character must be letter or underscore
    if not (name[0].isalpha() or name[0] == '_'):
        # Special case: allow names starting with . for commands like '..'
        if name[0] != '.':
            return False

    # Rest can be alphanumeric, underscore, hyphen, or dot
    for char in name:
        if not (char.isalnum() or char in '_-.'):
            return False

    return True
