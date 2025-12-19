"""Main CLI entry point for agfs-shell"""

import sys
import os
import argparse
from .shell import Shell
from .config import Config
from .exit_codes import (
    EXIT_CODE_FOR_LOOP_NEEDED,
    EXIT_CODE_WHILE_LOOP_NEEDED,
    EXIT_CODE_IF_STATEMENT_NEEDED,
    EXIT_CODE_HEREDOC_NEEDED,
    EXIT_CODE_FUNCTION_DEF_NEEDED
)


def execute_agfs_script(shell, agfs_path, script_args=None, silent=False):
    """Execute a script file from AGFS filesystem line by line

    This is a wrapper around shell.execute_script() for backward compatibility.

    Args:
        shell: Shell instance
        agfs_path: Path to script file in AGFS
        script_args: List of arguments to pass to script (accessible as $1, $2, etc.)
        silent: If True, suppress error messages for missing files

    Returns:
        Exit code from script execution, or None if file not found
    """
    return shell.execute_script(agfs_path, script_args=script_args, silent=silent)


# List of initrc files to check on startup (in order)
INITRC_FILES = [
    '/etc/rc',
    '/etc/initrc',
    '/initrc.as',
    '/etc/profile',
    '/etc/profile.as',
]


def execute_initrc_scripts(shell):
    """Execute initrc scripts from AGFS filesystem on shell startup

    Checks for initrc files in order and executes any that exist.
    Errors in initrc scripts are reported but do not prevent shell startup.

    Args:
        shell: Shell instance
    """
    for initrc_path in INITRC_FILES:
        result = execute_agfs_script(shell, initrc_path, silent=True)
        if result is not None and result != 0:
            # Script existed but had an error - report but continue
            sys.stderr.write(f"agfs-shell: warning: {initrc_path} returned exit code {result}\n")


def execute_script_file(shell, script_path, script_args=None):
    """Execute a local script file.

    This is a wrapper around shell.execute_script_content() for local files.

    Args:
        shell: Shell instance
        script_path: Path to local script file
        script_args: List of arguments to pass to script (accessible as $1, $2, etc.)

    Returns:
        Exit code from script execution
    """
    try:
        with open(script_path, 'r') as f:
            content = f.read()
        return shell.execute_script_content(content, script_name=script_path, script_args=script_args)
    except FileNotFoundError:
        sys.stderr.write(f"agfs-shell: {script_path}: No such file or directory\n")
        return 127
    except PermissionError:
        sys.stderr.write(f"agfs-shell: {script_path}: Permission denied\n")
        return 126
    except Exception as e:
        sys.stderr.write(f"agfs-shell: {script_path}: {str(e)}\n")
        return 1


def main():
    """Main entry point for the shell"""
    # Parse command line arguments
    parser = argparse.ArgumentParser(
        description='agfs-shell - Experimental shell with AGFS integration',
        add_help=False  # We'll handle help ourselves
    )
    parser.add_argument('--agfs-api-url',
                        dest='agfs_api_url',
                        help='AGFS API URL (default: http://localhost:8080 or $AGFS_API_URL)',
                        default=None)
    parser.add_argument('--timeout',
                        dest='timeout',
                        type=int,
                        help='Request timeout in seconds (default: 30 or $AGFS_TIMEOUT)',
                        default=None)
    parser.add_argument('-c',
                        dest='command_string',
                        help='Execute command string',
                        default=None)
    parser.add_argument('--help', '-h', action='store_true',
                        help='Show this help message')
    parser.add_argument('--webapp',
                        action='store_true',
                        help='Start web application server')
    parser.add_argument('--webapp-host',
                        dest='webapp_host',
                        default='localhost',
                        help='Web app host (default: localhost)')
    parser.add_argument('--webapp-port',
                        dest='webapp_port',
                        type=int,
                        default=3000,
                        help='Web app port (default: 3000)')
    parser.add_argument('script', nargs='?', help='Script file to execute')
    parser.add_argument('args', nargs='*', help='Arguments to script (or command if no script)')

    # Use parse_known_args to allow command-specific flags to pass through
    args, unknown = parser.parse_known_args()

    # Merge unknown args with args - they should all be part of the command
    if unknown:
        # Insert unknown args at the beginning since they came before positional args
        args.args = unknown + args.args

    # Show help if requested
    if args.help:
        parser.print_help()
        sys.exit(0)

    # Create configuration
    config = Config.from_args(server_url=args.agfs_api_url, timeout=args.timeout)

    # Initialize shell with configuration
    shell = Shell(server_url=config.server_url, timeout=config.timeout)

    # Check if webapp mode is requested
    if args.webapp:
        # Start web application server
        try:
            from .webapp_server import run_server
            run_server(shell, host=args.webapp_host, port=args.webapp_port)
        except ImportError as e:
            sys.stderr.write(f"Error: Web app dependencies not installed.\n")
            sys.stderr.write(f"Install with: uv sync --extra webapp\n")
            sys.exit(1)
        except Exception as e:
            sys.stderr.write(f"Error starting web app: {e}\n")
            sys.exit(1)
        return

    # Execute initrc scripts from AGFS filesystem
    # These scripts can set up environment, define functions, aliases, etc.
    execute_initrc_scripts(shell)

    # Determine mode of execution
    # Priority: -c flag > script file > command args > interactive

    if args.command_string:
        # Mode 1: -c "command string"
        command = args.command_string
        stdin_data = None
        import re
        import select
        has_input_redir = bool(re.search(r'\s<\s', command))
        if not sys.stdin.isatty() and not has_input_redir:
            if select.select([sys.stdin], [], [], 0.0)[0]:
                stdin_data = sys.stdin.buffer.read()

        # Check if command contains semicolons (multiple commands)
        # Split intelligently: respect if/then/else/fi, for/do/done blocks, and functions
        if ';' in command:
            # Smart split that tracks brace depth for functions
            import re
            commands = []
            current_cmd = []
            in_control_flow = False
            control_flow_type = None
            brace_depth = 0

            for part in command.split(';'):
                part = part.strip()
                if not part:
                    continue

                # Track brace depth for functions
                brace_depth += part.count('{') - part.count('}')

                # Check if this part starts a control flow statement or function
                if not in_control_flow:
                    if part.startswith('if '):
                        in_control_flow = True
                        control_flow_type = 'if'
                        current_cmd.append(part)
                    elif part.startswith('for '):
                        in_control_flow = True
                        control_flow_type = 'for'
                        current_cmd.append(part)
                    elif part.startswith('while '):
                        in_control_flow = True
                        control_flow_type = 'while'
                        current_cmd.append(part)
                    elif re.match(r'^([A-Za-z_][A-Za-z0-9_]*)\s*\(\)', part) or part.startswith('function '):
                        # Function definition
                        current_cmd.append(part)
                        if brace_depth == 0 and '}' in part:
                            # Complete single-line function (e.g., "foo() { echo hi; }")
                            commands.append('; '.join(current_cmd))
                            current_cmd = []
                        else:
                            in_control_flow = True
                            control_flow_type = 'function'
                    else:
                        # Regular command
                        commands.append(part)
                else:
                    # We're in a control flow statement
                    current_cmd.append(part)
                    # Check if this part ends the control flow statement
                    ended = False
                    if control_flow_type == 'if' and part.strip() == 'fi':
                        ended = True
                    elif control_flow_type == 'for' and part.strip() == 'done':
                        ended = True
                    elif control_flow_type == 'while' and part.strip() == 'done':
                        ended = True
                    elif control_flow_type == 'function' and brace_depth == 0:
                        ended = True

                    if ended:
                        commands.append('; '.join(current_cmd))
                        current_cmd = []
                        in_control_flow = False
                        control_flow_type = None

            # Add any remaining command
            if current_cmd:
                commands.append('; '.join(current_cmd))

            # Execute each command in sequence
            exit_code = 0
            for cmd in commands:
                exit_code = shell.execute(cmd, stdin_data=stdin_data)
                stdin_data = None  # Only first command gets stdin
                if exit_code != 0 and exit_code not in [
                    EXIT_CODE_FOR_LOOP_NEEDED,
                    EXIT_CODE_WHILE_LOOP_NEEDED,
                    EXIT_CODE_IF_STATEMENT_NEEDED,
                    EXIT_CODE_HEREDOC_NEEDED,
                    EXIT_CODE_FUNCTION_DEF_NEEDED
                ]:
                    # Stop on error (unless it's a special code)
                    break
            sys.exit(exit_code)
        else:
            # Single command
            exit_code = shell.execute(command, stdin_data=stdin_data)
            sys.exit(exit_code)

    elif args.script and os.path.isfile(args.script):
        # Mode 2: script file
        exit_code = execute_script_file(shell, args.script, script_args=args.args)
        sys.exit(exit_code)

    elif args.script:
        # Mode 3: command with arguments
        command_parts = [args.script] + args.args
        command = ' '.join(command_parts)
        stdin_data = None
        import re
        import select
        has_input_redir = bool(re.search(r'\s<\s', command))
        if not sys.stdin.isatty() and not has_input_redir:
            if select.select([sys.stdin], [], [], 0.0)[0]:
                stdin_data = sys.stdin.buffer.read()
        exit_code = shell.execute(command, stdin_data=stdin_data)
        sys.exit(exit_code)

    else:
        # Mode 4: Interactive REPL
        shell.repl()


if __name__ == '__main__':
    main()
