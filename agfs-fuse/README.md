# AGFS FUSE [WIP]

A FUSE filesystem implementation for mounting AGFS servers on Linux.

## Platform Support

Currently supports **Linux only**.

## Prerequisites

- Go 1.21.1 or higher
- FUSE development libraries
- Linux kernel with FUSE support

Install FUSE on your system:
```bash
# Debian/Ubuntu
sudo apt-get install fuse3 libfuse3-dev

# RHEL/Fedora/CentOS
sudo dnf install fuse3 fuse3-devel

# Arch Linux
sudo pacman -S fuse3
```

## Quick Start

### Build

```bash
# Using Makefile (recommended)
make build

# Or build directly with Go
go build -o build/agfs-fuse ./cmd/agfs-fuse
```

### Install (Optional)

```bash
# Install to /usr/local/bin
make install
```

### Mount

```bash
# Basic usage
./build/agfs-fuse --agfs-server-url http://localhost:8080 --mount /mnt/agfs

# With custom cache TTL
./build/agfs-fuse --agfs-server-url http://localhost:8080 --mount /mnt/agfs --cache-ttl=10s

# Enable debug output
./build/agfs-fuse --agfs-server-url http://localhost:8080 --mount /mnt/agfs --debug

# Allow other users to access the mount
./build/agfs-fuse --agfs-server-url http://localhost:8080 --mount /mnt/agfs --allow-other
```

### Unmount

Press `Ctrl+C` in the terminal where agfs-fuse is running, or use:
```bash
fusermount3 -u /mnt/agfs
# or, on systems where fuse3 installs the legacy command name:
fusermount -u /mnt/agfs
```

## Troubleshooting

- Verify the server first: `curl -sf http://localhost:8080/api/v1/health`.
- `fusermount3: command not found`: install FUSE 3 packages, for example `sudo apt-get install fuse3 libfuse3-dev` on Debian/Ubuntu.
- `failed to open /dev/fuse`: FUSE is not available to the process. On Docker/Linux, pass `--device /dev/fuse --cap-add SYS_ADMIN --security-opt apparmor:unconfined`; Docker Desktop on macOS does not support this mount path.
- `operation not permitted` with `--allow-other`: ensure `/etc/fuse.conf` contains `user_allow_other`, or mount without `--allow-other`.
- Empty or stale listings: confirm the same server URL with `--agfs-server-url`, then lower `--cache-ttl` while debugging.
- Unmount reports the target is busy: close processes using the mount, then run `lsof +f -- /mnt/agfs` to find remaining users.

For the full AGFS first-run matrix, see [../docs/first-run.md](../docs/first-run.md).

## Usage

```
agfs-fuse [options]

Options:
  -agfs-server-url string
        AGFS server URL (required)
  -mount string
        Mount point directory (required)
  -cache-ttl duration
        Cache TTL duration (default 5s)
  -debug
        Enable debug output
  -allow-other
        Allow other users to access the mount
  -version
        Show version information
```

## License

See LICENSE file for details.
