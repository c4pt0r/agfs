"""
FSGREP command - server-side grep using filesystem's custom implementation.
Supports VectorFS semantic search and other custom grep implementations.
"""

import os
from ..process import Process
from ..command_decorators import command
from . import register_command
from pyagfs import AGFSClientError


@command(supports_streaming=True)
@register_command('fsgrep')
def cmd_fsgrep(process: Process) -> int:
    """
    Search using server-side filesystem grep (supports VectorFS semantic search)

    Usage: fsgrep [OPTIONS] PATTERN PATH

    Options:
        -r          Recursive search (default for directories)
        -i          Case insensitive (for text grep, not VectorFS)
        -n          Show line numbers (default)
        -c          Count matches only
        -q          Quiet mode (only show if matches found)

    For VectorFS paths (/vectorfs/...), PATTERN is a semantic query.
    For other filesystems, PATTERN is a regular expression.

    Examples:
        # VectorFS semantic search
        fsgrep "container orchestration" /vectorfs/project/docs
        fsgrep -r "infrastructure automation" /vectorfs/namespace/docs

        # Regular text grep on other filesystems
        fsgrep "error" /local/tmp/app.log
        fsgrep -i "warning" /s3fs/aws/logs/
        fsgrep -c "TODO" /sqlfs/tidb/code/

    VectorFS Features:
        - Semantic search using embeddings
        - Returns results ranked by relevance
        - Automatically searches all documents in namespace
    """
    # Parse options
    recursive = False
    case_insensitive = False
    show_line_numbers = True  # Default to showing line numbers
    count_only = False
    quiet = False

    args = process.args[:]

    while args and args[0].startswith('-') and args[0] != '-':
        opt = args.pop(0)
        if opt == '--':
            break

        for char in opt[1:]:
            if char == 'r':
                recursive = True
            elif char == 'i':
                case_insensitive = True
            elif char == 'n':
                show_line_numbers = True
            elif char == 'c':
                count_only = True
            elif char == 'q':
                quiet = True
            else:
                process.stderr.write(f"fsgrep: invalid option -- '{char}'\n")
                return 2

    # Get pattern and path
    if len(args) < 2:
        process.stderr.write("Usage: fsgrep [OPTIONS] PATTERN PATH\n")
        return 2

    pattern = args[0]
    path = args[1]

    # Normalize path
    if not path.startswith('/'):
        # Convert relative path to absolute
        cwd = getattr(process, 'cwd', '/')
        if path == '.':
            # Current directory
            path = cwd
        elif path == '..':
            # Parent directory
            path = os.path.dirname(cwd) or '/'
        else:
            # Relative path
            path = os.path.join(cwd, path)

    # Clean up the path (remove ./ and ../ and normalize slashes)
    path = os.path.normpath(path)

    # Ensure path starts with / (normpath might remove it)
    if not path.startswith('/'):
        path = '/' + path

    # Determine if path is VectorFS
    is_vectorfs = path.startswith('/vectorfs')

    # VectorFS is always recursive for docs search
    if is_vectorfs:
        recursive = True

    try:
        # Call server-side grep (no need to stat path first)
        result = process.filesystem.grep(
            path=path,
            pattern=pattern,
            recursive=recursive,
            case_insensitive=case_insensitive,
            stream=False
        )

        matches = result.get('matches', [])
        count = result.get('count', 0)

        if count_only:
            process.stdout.write(f"{count}\n")
            return 0 if count > 0 else 1

        if quiet:
            return 0 if count > 0 else 1

        if count == 0:
            if not quiet:
                process.stderr.write("No matches found\n")
            return 1

        # Display results
        for match in matches:
            file_path = match.get('file', '')
            line_num = match.get('line', 0)
            content = match.get('content', '')
            metadata = match.get('metadata', {})

            # Build output
            if show_line_numbers:
                output = f"\033[35m{file_path}\033[0m:\033[32m{line_num}\033[0m: {content}"
            else:
                output = f"\033[35m{file_path}\033[0m: {content}"

            # Add metadata for VectorFS results
            if metadata and is_vectorfs:
                score = metadata.get('score', metadata.get('distance', None))
                if score is not None:
                    if 'score' in metadata:
                        # score is similarity (higher is better)
                        output += f" \033[90m[score: {score:.3f}]\033[0m"
                    else:
                        # distance (lower is better)
                        output += f" \033[90m[distance: {score:.3f}]\033[0m"

            process.stdout.write(output + "\n")

        # Show summary for VectorFS
        if is_vectorfs and count > 0:
            process.stdout.write(f"\n\033[90mFound {count} semantically relevant results\033[0m\n")

        return 0

    except AGFSClientError as e:
        error_msg = str(e)
        if "not support custom grep" in error_msg.lower():
            process.stderr.write(f"fsgrep: {path} does not support server-side grep\n")
            process.stderr.write("Use regular 'grep' command for text search\n")
        else:
            process.stderr.write(f"fsgrep: {error_msg}\n")
        return 1
    except Exception as e:
        process.stderr.write(f"fsgrep: error: {e}\n")
        return 1
