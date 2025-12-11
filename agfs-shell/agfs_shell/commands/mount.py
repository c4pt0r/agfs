"""
MOUNT command - mount a plugin dynamically or list mounted filesystems.
"""

from ..process import Process
from ..command_decorators import command
from . import register_command


@command()
@register_command('mount')
def cmd_mount(process: Process) -> int:
    """
    Mount a plugin dynamically or list mounted filesystems

    Usage: mount [<fstype> <path> [key=value ...]]

    Without arguments: List all mounted filesystems
    With arguments: Mount a new filesystem

    Examples:
        mount                    # List all mounted filesystems
        mount memfs /test/mem
        mount sqlfs /test/db backend=sqlite db_path=/tmp/test.db
        mount s3fs /test/s3 bucket=my-bucket region=us-west-1 access_key_id=xxx secret_access_key=yyy
        mount proxyfs /remote "base_url=http://workstation:8080/api/v1"  # Quote URLs with colons
    """
    if not process.filesystem:
        process.stderr.write("mount: filesystem not available\n")
        return 1

    # No arguments - list mounted filesystems
    if len(process.args) == 0:
        try:
            mounts_list = process.filesystem.client.mounts()

            if not mounts_list:
                process.stdout.write("No plugins mounted\n")
                return 0

            # Print mounts in Unix mount style: <fstype> on <mountpoint> (options...)
            for mount in mounts_list:
                path = mount.get("path", "")
                plugin = mount.get("pluginName", "")
                config = mount.get("config", {})

                # Build options string from config
                options = []
                for key, value in config.items():
                    # Hide sensitive keys
                    if key in ["secret_access_key", "password", "token"]:
                        options.append(f"{key}=***")
                    else:
                        # Convert value to string, truncate if too long
                        value_str = str(value)
                        if len(value_str) > 50:
                            value_str = value_str[:47] + "..."
                        options.append(f"{key}={value_str}")

                # Format output line
                if options:
                    options_str = ", ".join(options)
                    process.stdout.write(f"{plugin} on {path} (plugin: {plugin}, {options_str})\n")
                else:
                    process.stdout.write(f"{plugin} on {path} (plugin: {plugin})\n")

            return 0
        except Exception as e:
            error_msg = str(e)
            process.stderr.write(f"mount: {error_msg}\n")
            return 1

    # With arguments - mount a new filesystem
    if len(process.args) < 2:
        process.stderr.write("mount: missing operands\n")
        process.stderr.write("Usage: mount <fstype> <path> [key=value ...]\n")
        process.stderr.write("\nExamples:\n")
        process.stderr.write("  mount memfs /test/mem\n")
        process.stderr.write("  mount sqlfs /test/db backend=sqlite db_path=/tmp/test.db\n")
        process.stderr.write("  mount s3fs /test/s3 bucket=my-bucket region=us-west-1\n")
        process.stderr.write('  mount proxyfs /remote "base_url=http://workstation:8080/api/v1"  # Quote URLs\n')
        return 1

    fstype = process.args[0]
    path = process.args[1]
    config_args = process.args[2:] if len(process.args) > 2 else []

    # Parse key=value config arguments
    config = {}
    for arg in config_args:
        if '=' in arg:
            key, value = arg.split('=', 1)
            config[key.strip()] = value.strip()
        else:
            process.stderr.write(f"mount: invalid config argument: {arg}\n")
            process.stderr.write("Config arguments must be in key=value format\n")
            return 1

    try:
        # Use AGFS client to mount the plugin
        process.filesystem.client.mount(fstype, path, config)
        process.stdout.write(f"Mounted {fstype} at {path}\n")
        return 0
    except Exception as e:
        error_msg = str(e)
        process.stderr.write(f"mount: {error_msg}\n")
        return 1
