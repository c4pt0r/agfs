"""Main CLI entry point for agfs-shell"""

import sys
import os
import argparse
from .shell import Shell
from .config import Config
from .exit_codes import (
    EXIT_CODE_CONTINUE,
    EXIT_CODE_BREAK,
    EXIT_CODE_FOR_LOOP_NEEDED,
    EXIT_CODE_IF_STATEMENT_NEEDED,
    EXIT_CODE_HEREDOC_NEEDED
)


def execute_script_file(shell, script_path, script_args=None):
    """Execute a script file line by line

    Args:
        shell: Shell instance
        script_path: Path to script file
        script_args: List of arguments to pass to script (accessible as $1, $2, etc.)
    """
    # Set script name and arguments as environment variables
    shell.env['0'] = script_path  # Script name

    if script_args:
        for i, arg in enumerate(script_args, start=1):
            shell.env[str(i)] = arg
        shell.env['#'] = str(len(script_args))
        shell.env['@'] = ' '.join(script_args)
    else:
        shell.env['#'] = '0'
        shell.env['@'] = ''

    try:
        with open(script_path, 'r') as f:
            lines = f.readlines()

        exit_code = 0
        i = 0
        while i < len(lines):
            line = lines[i].strip()
            line_num = i + 1

            # Skip empty lines and comments
            if not line or line.startswith('#'):
                i += 1
                continue

            # Execute the command
            try:
                exit_code = shell.execute(line)

                # Check if for-loop needs to be collected
                if exit_code == EXIT_CODE_FOR_LOOP_NEEDED:
                    # Collect for/do/done loop
                    for_lines = [line]
                    for_depth = 1  # Track nesting depth
                    i += 1
                    while i < len(lines):
                        next_line = lines[i].strip()
                        for_lines.append(next_line)
                        # Strip comments before checking keywords
                        next_line_no_comment = shell._strip_comment(next_line).strip()
                        # Count nested for loops
                        if next_line_no_comment.startswith('for '):
                            for_depth += 1
                        elif next_line_no_comment == 'done':
                            for_depth -= 1
                            if for_depth == 0:
                                break
                        i += 1

                    # Execute the for loop
                    exit_code = shell.execute_for_loop(for_lines)
                    # continue/break are normal control flow, not errors
                    if exit_code != 0 and exit_code not in [EXIT_CODE_CONTINUE, EXIT_CODE_BREAK]:
                        sys.stderr.write(f"Error at line {line_num}: for loop failed with exit code {exit_code}\n")
                        return exit_code
                    # Reset control flow codes to 0 for script execution
                    if exit_code in [EXIT_CODE_CONTINUE, EXIT_CODE_BREAK]:
                        exit_code = 0
                # Check if if-statement needs to be collected
                elif exit_code == EXIT_CODE_IF_STATEMENT_NEEDED:
                    # Collect if/then/else/fi statement with depth tracking
                    if_lines = [line]
                    if_depth = 1  # Track nesting depth
                    i += 1
                    while i < len(lines):
                        next_line = lines[i].strip()
                        if_lines.append(next_line)
                        # Strip comments before checking keywords
                        next_line_no_comment = shell._strip_comment(next_line).strip()
                        # Track nested if statements
                        if next_line_no_comment.startswith('if '):
                            if_depth += 1
                        elif next_line_no_comment == 'fi':
                            if_depth -= 1
                            if if_depth == 0:
                                break
                        i += 1

                    # Execute the if statement
                    exit_code = shell.execute_if_statement(if_lines)
                    if exit_code != 0:
                        sys.stderr.write(f"Error at line {line_num}: if statement failed with exit code {exit_code}\n")
                        return exit_code
                # If a command fails, stop execution
                elif exit_code != 0:
                    sys.stderr.write(f"Error at line {line_num}: command failed with exit code {exit_code}\n")
                    return exit_code
            except SystemExit as e:
                # Handle exit command - return the exit code
                return e.code if e.code is not None else 0
            except Exception as e:
                sys.stderr.write(f"Error at line {line_num}: {str(e)}\n")
                return 1

            i += 1

        return exit_code
    except SystemExit as e:
        # Handle exit command at top level
        return e.code if e.code is not None else 0
    except FileNotFoundError:
        sys.stderr.write(f"agfs-shell: {script_path}: No such file or directory\n")
        return 127
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
        # Split intelligently: respect if/then/else/fi and for/do/done blocks
        if ';' in command:
            # Split by semicolons but preserve control flow statements as single units
            commands = []
            current_cmd = []
            in_control_flow = False
            control_flow_type = None  # 'if' or 'for'

            for part in command.split(';'):
                part = part.strip()
                if not part:
                    continue

                # Check if this part starts a control flow statement
                if part.startswith('if '):
                    in_control_flow = True
                    control_flow_type = 'if'
                    current_cmd.append(part)
                elif part.startswith('for '):
                    in_control_flow = True
                    control_flow_type = 'for'
                    current_cmd.append(part)
                # Check if we're in a control flow statement
                elif in_control_flow:
                    current_cmd.append(part)
                    # Check if this part ends the control flow statement
                    if (control_flow_type == 'if' and 'fi' in part) or \
                       (control_flow_type == 'for' and 'done' in part):
                        # Complete control flow statement
                        commands.append('; '.join(current_cmd))
                        current_cmd = []
                        in_control_flow = False
                        control_flow_type = None
                else:
                    # Regular command
                    if current_cmd:
                        commands.append('; '.join(current_cmd))
                        current_cmd = []
                    commands.append(part)

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
                    EXIT_CODE_IF_STATEMENT_NEEDED,
                    EXIT_CODE_HEREDOC_NEEDED
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
