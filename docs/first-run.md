# AGFS First-Run Guide

This guide collects the supported first-run paths and the fastest checks for deciding whether the server, shell, webapp, FUSE mount, or MCP server is ready.

## First-Run Matrix

| Path | Use when | Prerequisites | Start command | Verify | Common next step |
| --- | --- | --- | --- | --- | --- |
| Installer with systemd | Linux host where AGFS should run as a service | `curl`, `sudo`, systemd | `curl -fsSL https://raw.githubusercontent.com/c4pt0r/agfs/master/install.sh \| sh` then `sudo systemctl start agfs-server` | `curl -sf http://localhost:8080/api/v1/health` | Inspect `sudo systemctl status agfs-server`; service config is `/etc/agfs.yaml`. |
| Installer without systemd | Local user install or non-systemd host | `curl`, writable `~/.local/bin` | `curl -fsSL https://raw.githubusercontent.com/c4pt0r/agfs/master/install.sh \| sh` then `agfs-server -c ~/.config/agfs/config.yaml` | `curl -sf http://localhost:8080/api/v1/health` | Add `~/.local/bin` to `PATH` if `agfs-server` or `agfs` is not found. |
| Source server | Development checkout | Go toolchain | `cd agfs-server && make dev` | `curl -sf http://localhost:8080/api/v1/health` | Copy `config.example.yaml` to `config.yaml` only for local edits; `make dev` falls back to the example config on a fresh checkout. |
| Docker HTTP server | Quick server without local Go build | Docker | `docker run --rm -p 8080:8080 -e SKIP_FUSE_MOUNT=true c4pt0r/agfs:latest` | `curl -sf http://localhost:8080/api/v1/health` | Mount a host config with `-v "$(pwd)/config.yaml:/config.yaml"` when needed. |
| agfs-shell | Interactive file operations against a running server | Python 3.10+, `uv`, reachable AGFS server | `cd agfs-shell && uv sync && uv run agfs-shell` | `uv run agfs-shell -c "ls /"` | Set `AGFS_API_URL=http://host:8080` or pass `--agfs-api-url` for non-local servers. |
| Integrated webapp | Browser UI served by `agfs-shell` | Python 3.10+, `uv`, Node.js/npm, reachable AGFS server | `cd agfs-shell/webapp && npm ci && npm run build`; then `cd .. && uv sync --extra webapp && uv run agfs-shell --webapp` | Open `http://localhost:3000` | If startup reports missing `webapp/dist/index.html`, run the frontend build step again or `cd agfs-shell/webapp && ./setup.sh`. |
| Webapp dev server | Frontend-only development with Vite proxy | Node.js/npm, AGFS server at `localhost:8080` | `cd agfs-shell/webapp && npm ci && npm run dev` | Open the Vite localhost URL | Keep Vite bound to localhost unless the dev server exposure has been reviewed. |
| FUSE mount | Native filesystem mount on Linux | Linux, FUSE 3 packages, built `agfs-fuse`, reachable AGFS server | `cd agfs-fuse && make build && mkdir -p /tmp/agfs && ./build/agfs-fuse --agfs-server-url http://localhost:8080 --mount /tmp/agfs` | `ls /tmp/agfs` | Unmount with `fusermount3 -u /tmp/agfs` or `fusermount -u /tmp/agfs`. |
| MCP server | Expose AGFS tools to an MCP client | Python 3.10+, `uv`, reachable AGFS server | `cd agfs-mcp && uv sync && AGFS_SERVER_URL=http://localhost:8080 uv run agfs-mcp` | In the MCP client, call `tools/list` or `agfs_health` | The MCP process speaks JSON-RPC over stdio; do not expect an HTTP port. |

## Health Checks

Use these checks before debugging a client:

```bash
curl -sf http://localhost:8080/api/v1/health
curl -sf "http://localhost:8080/api/v1/directories?path=/"
cd agfs-shell && AGFS_API_URL=http://localhost:8080 uv run agfs-shell -c "ls /"
```

If the server runs on another host or port, use the same base URL everywhere:

```bash
export AGFS_API_URL=http://127.0.0.1:8080
export AGFS_SERVER_URL=http://127.0.0.1:8080
```

`AGFS_API_URL` is the preferred shell variable. `AGFS_SERVER_URL` is used by MCP and is also accepted by shell configuration for compatibility. Set them independently when shell and MCP should talk to different AGFS servers.

## Configuration Paths

- `agfs-server/config.example.yaml` is the repository example. Keep it read-only and unchanged.
- `agfs-server/config.yaml` is the source-development customization file. Create it with `cp config.example.yaml config.yaml` only when you want local edits.
- `~/.config/agfs/config.yaml` is the installer-created direct-run config for `agfs-server -c ~/.config/agfs/config.yaml`.
- `/etc/agfs.yaml` is the installer-created systemd service config on Linux.

## FUSE Troubleshooting

- `fusermount: command not found`: install FUSE 3 packages. On Debian/Ubuntu: `sudo apt-get install fuse3 libfuse3-dev`.
- `Mount failed: fusermount3: failed to open /dev/fuse`: load or expose FUSE on the host. In Docker, pass `--device /dev/fuse --cap-add SYS_ADMIN --security-opt apparmor:unconfined`; this is not supported by Docker Desktop on macOS.
- `Error: --mount is required`: pass a real mount directory, for example `mkdir -p /tmp/agfs && ./build/agfs-fuse --mount /tmp/agfs`.
- `connection refused` or empty root listing: verify `curl -sf http://localhost:8080/api/v1/health` first and pass the same URL with `--agfs-server-url`.
- `operation not permitted` with `--allow-other`: ensure `/etc/fuse.conf` contains `user_allow_other`, or mount without `--allow-other`.
- Stale listings after writes: lower `--cache-ttl`, or remount while debugging.
- Unmount hangs or says target is busy: close processes using the mount, then run `fusermount3 -u /tmp/agfs` or `fusermount -u /tmp/agfs`. Use `lsof +f -- /tmp/agfs` to find open users.

## MCP Troubleshooting

- MCP starts but tools fail with connection errors: start AGFS first and verify `curl -sf http://localhost:8080/api/v1/health`.
- The MCP client cannot find `agfs-mcp`: use an absolute command path, or configure `uvx --from /path/to/agfs-mcp agfs-mcp` while developing from a checkout.
- The client points at the wrong server: set `AGFS_SERVER_URL=http://host:8080` in the MCP client environment.
- `tools/list` works but file tools fail: confirm the target path exists with `curl -sf "http://localhost:8080/api/v1/directories?path=/"` and check plugin mount availability.
- Nothing appears in a browser: MCP is stdio JSON-RPC for MCP clients, not an HTTP web server.

## Webapp Troubleshooting

- Integrated webapp exits before serving: build `agfs-shell/webapp/dist` with `npm ci && npm run build`, or run `./setup.sh` from `agfs-shell/webapp`.
- Browser loads but API calls fail: verify the AGFS server health endpoint and set `AGFS_API_URL=http://host:8080` before starting `uv run agfs-shell --webapp`.
- Vite dev server API calls fail: by default the Vite proxy expects AGFS at `http://localhost:8080`; start the server there or update `agfs-shell/webapp/vite.config.js` for local development.
