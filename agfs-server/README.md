# AGFS Server

A Plugin-based RESTful file system server with a powerful plugin architecture that exposes services as virtual file systems. Access queues, key-value stores, databases, and more through simple file operations.

## Features

- **Plugin Architecture**: Mount multiple filesystems and services at different paths.
- **External Plugin Support**: Load plugins from dynamic libraries (.so/.dylib/.dll) or WebAssembly modules without recompiling.
- **Unified API**: Single HTTP API for all file operations across all plugins.
- **Dynamic Mounting**: Add/remove plugins at runtime without restarting.
- **Configuration-based**: YAML configuration supports both single and multi-instance plugins.
- **Built-in Plugins**: Includes various useful plugins like QueueFS, KVFS, S3FS, SQLFS, and more.
- **Zero Cgo for Native Plugins**: Uses purego for FFI, eliminating the need for a C compiler for Go code.

## Quick Start

### Using Docker (Recommended)

The easiest way to get started is using Docker:

1.  **Pull the image**:
    ```bash
    docker pull c4pt0r/agfs-server:latest
    ```

2.  **Run the server with port mapping**:
    ```bash
    # Basic run - expose port 8080 to host
    docker run -d -p 8080:8080 --name agfs-server c4pt0r/agfs-server:latest

    # With custom port mapping (host:container)
    docker run -d -p 9000:8080 --name agfs-server c4pt0r/agfs-server:latest

    # With data persistence (mount /data directory)
    docker run -d -p 8080:8080 -v $(pwd)/data:/data --name agfs-server c4pt0r/agfs-server:latest

    # With custom configuration
    docker run -d -p 8080:8080 -v $(pwd)/config.yaml:/config.yaml --name agfs-server c4pt0r/agfs-server:latest
    ```

3.  **Using agfs-shell inside the container**:

    The Docker image includes `agfs-shell` for convenient file system operations.

    ```bash
    # Enter the container with interactive shell
    docker exec -it agfs-server /bin/sh

    # Inside the container, use agfs-shell
    agfs-shell

    # Or run agfs-shell commands directly
    docker exec -it agfs-server agfs-shell -c "ls /"
    docker exec -it agfs-server agfs-shell -c "cat /memfs/hello.txt"
    ```

4.  **Verify the server is running**:
    ```bash
    curl http://localhost:8080/api/v1/health
    ```

5.  **Stop and remove the container**:
    ```bash
    docker stop agfs-server
    docker rm agfs-server
    ```

### Build and Run from Source

1.  **Build the server**:
    ```bash
    make build
    ```

2.  **Run with default configuration** (port 8080):
    ```bash
    ./build/agfs-server
    ```

3.  **Run with custom configuration**:
    ```bash
    ./build/agfs-server -c config.yaml
    ```

4.  **Run on a different port**:
    ```bash
    ./build/agfs-server -addr :9000
    ```

### Basic Usage

You can interact with the server using standard HTTP clients like `curl` or the `agfs-shell` (if available).

**List root directory**:
```bash
curl "http://localhost:8080/api/v1/directories?path=/"
```

**Write to a file (MemFS example)**:
```bash
curl -X PUT "http://localhost:8080/api/v1/files?path=/memfs/hello.txt" -d "Hello, AGFS!"
```

**Read a file**:
```bash
curl "http://localhost:8080/api/v1/files?path=/memfs/hello.txt"
```

## Configuration

The server is configured using a YAML file (default: `config.yaml`).

### Structure

```yaml
server:
  address: ":8080"
  log_level: info  # debug, info, warn, error

# External plugins configuration
external_plugins:
  enabled: true
  plugin_dir: "./plugins"        # Auto-load plugins from this directory
  auto_load: true
  plugin_paths:                  # Specific plugins to load
    - "./examples/hellofs-c/hellofs-c.dylib"

plugins:
  # Single instance configuration
  memfs:
    enabled: true
    path: /memfs
    config:
      init_dirs:
        - /tmp

  # Multi-instance configuration
  sqlfs:
    - name: local
      enabled: true
      path: /sqlfs
      config:
        backend: sqlite
        db_path: sqlfs.db

    - name: production
      enabled: true
      path: /sqlfs_prod
      config:
        backend: tidb
        dsn: "user:pass@tcp(host:4000)/db"
```

See `config.example.yaml` for a complete reference.

## Built-in Plugins

AGFS Server comes with a rich set of built-in plugins.

### Storage Plugins

-   **MemFS**: In-memory file system. Fast, non-persistent storage ideal for temporary data and caching.
-   **LocalFS**: Mounts local directories into the AGFS namespace. Allows direct access to the host file system.
-   **S3FS**: Exposes Amazon S3 buckets as a file system. Supports reading, writing, and listing objects.
-   **SQLFS**: Database-backed file system. Stores files and metadata in SQL databases (SQLite, TiDB, MySQL).

### Application Plugins

-   **QueueFS**: Exposes message queues as directories.
    -   `enqueue`: Write to add a message.
    -   `dequeue`: Read to pop a message.
    -   `peek`: Read to view the next message.
    -   `size`: Read to get queue size.
    -   Supports Memory, SQLite, and TiDB backends.
-   **KVFS**: Key-Value store where keys are files and values are file content.
-   **StreamFS**: Supports streaming data with multiple concurrent readers (Ring Buffer). Ideal for live video or data feeds.
-   **HeartbeatFS**: Heartbeat monitoring service.
    -   Create items with `mkdir`.
    -   Send heartbeats by touching `keepalive`.
    -   Monitor status via `ctl`.
    -   Items expire automatically if no heartbeat is received within the timeout.

### Network & Utility Plugins

-   **ProxyFS**: Federation plugin. Proxies requests to remote AGFS servers, allowing you to mount remote instances locally.
-   **HTTPFS** (HTTAGFS): Serves any AGFS path via HTTP. Browsable directory listings and file downloads. Can be mounted dynamically to temporarily share files.
-   **ServerInfoFS**: Exposes server metadata (version, uptime, stats) as files.
-   **HelloFS**: A simple example plugin for learning and testing.

## Dynamic Plugin Management

You can mount, unmount, and manage plugins at runtime using the API.

**Mount a plugin**:
```bash
curl -X POST http://localhost:8080/api/v1/mount \
  -H "Content-Type: application/json" \
  -d '{
    "fstype": "memfs",
    "path": "/temp_ram",
    "config": {}
  }'
```

**Unmount a plugin**:
```bash
curl -X POST http://localhost:8080/api/v1/unmount \
  -H "Content-Type: application/json" \
  -d '{"path": "/temp_ram"}'
```

**List mounted plugins**:
```bash
curl http://localhost:8080/api/v1/mounts
```

## External Plugins

AGFS Server supports loading external plugins compiled as shared libraries (`.so`, `.dylib`, `.dll`) or WebAssembly (`.wasm`) modules.

### Native Plugins (C/C++/Rust)
Native plugins must export a C-compatible API. They offer maximum performance and full system access.
See `examples/hellofs-c` or `examples/hellofs-rust` for implementation details.

### WebAssembly Plugins
WASM plugins run in a sandboxed environment (WasmTime). They are cross-platform and secure.
See `examples/hellofs-wasm` for implementation details.

### Loading External Plugins
```bash
curl -X POST http://localhost:8080/api/v1/plugins/load \
  -d '{"library_path": "./my-plugin.so"}'
```

## API Reference

All API endpoints are prefixed with `/api/v1/`.

| Resource | Method | Endpoint | Description |
|----------|--------|----------|-------------|
| **Files** | `GET` | `/files` | Read file content |
| | `PUT` | `/files` | Write file content |
| | `POST` | `/files` | Create empty file |
| | `DELETE` | `/files` | Delete file |
| | `GET` | `/stat` | Get file metadata |
| **Directories** | `GET` | `/directories` | List directory contents |
| | `POST` | `/directories` | Create directory |
| **Management** | `GET` | `/mounts` | List active mounts |
| | `POST` | `/mount` | Mount a plugin |
| | `POST` | `/unmount` | Unmount a plugin |
| | `GET` | `/plugins` | List loaded external plugins |
| | `POST` | `/plugins/load` | Load an external plugin |
| | `POST` | `/plugins/unload` | Unload an external plugin |
| **System** | `GET` | `/health` | Server health check |

## Development

### Requirements
-   Go 1.21+
-   Make

### Commands
-   `make build`: Build the server binary.
-   `make test`: Run tests.
-   `make dev`: Run the server in development mode.
-   `make install`: Install the binary to `$GOPATH/bin`.

## License

Apache License 2.0