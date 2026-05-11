# AGFS MCP Server

Model Context Protocol (MCP) server for AGFS (Plugin-based File System), enabling AI models to interact with AGFS through standardized tools.

## Overview

AGFS MCP Server exposes AGFS file system operations as MCP tools, allowing AI assistants like Claude to read, write, and manage files in a AGFS server through a standardized protocol.

## Features

- **File Operations**: Read, write, create, delete, copy, move files
- **Directory Operations**: List contents, create, remove, copy directories
- **Transfer Operations**: Upload from local filesystem to AGFS, download from AGFS to local filesystem
- **Search**: Grep with regex pattern matching
- **Plugin Management**: Mount/unmount plugins, list mounts
- **Health Monitoring**: Check server status
- **Notifications**: Send messages via QueueFS

## Installation

### Using uv (recommended)

```bash
# Install from local directory
uv pip install -e .

# Or if installing as dependency
uv pip install agfs-mcp
```

### Using pip

```bash
pip install -e .
```

## Usage

### Starting the Server

The MCP server runs as a stdio server that communicates via JSON-RPC:

```bash
# Using default AGFS server (http://localhost:8080)
agfs-mcp

# Using custom AGFS server URL
AGFS_SERVER_URL=http://myserver:8080 agfs-mcp
```

### Configuration with Claude Desktop

Add to your Claude Desktop configuration (`~/Library/Application Support/Claude/claude_desktop_config.json` on macOS):

```json
{
  "mcpServers": {
    "agfs": {
      "command": "agfs-mcp",
      "env": {
        "AGFS_SERVER_URL": "http://localhost:8080"
      }
    }
  }
}
```

Or if using uv:

```json
{
  "mcpServers": {
    "agfs": {
      "command": "uvx",
      "args": ["--from", "/path/to/agfs-mcp", "agfs-mcp"],
      "env": {
        "AGFS_SERVER_URL": "http://localhost:8080"
      }
    }
  }
}
```

### Available Tools

Once configured, the following tools are available to AI assistants:

#### File Operations

- `agfs_cat` - Read file content
  ```
  path: File path to read
  offset: Starting offset (optional, default: 0)
  size: Bytes to read (optional, default: -1 for all)
  ```

- `agfs_write` - Write content to file
  ```
  path: File path to write
  content: Content to write
  ```

- `agfs_rm` - Remove file or directory
  ```
  path: Path to remove
  recursive: Remove recursively (optional, default: false)
  ```

- `agfs_stat` - Get file/directory information
  ```
  path: Path to get info about
  ```

- `agfs_mv` - Move or rename file/directory
  ```
  old_path: Source path
  new_path: Destination path
  ```

- `agfs_cp` - Copy file or directory within AGFS
  ```
  src: Source path in AGFS
  dst: Destination path in AGFS
  recursive: Copy directories recursively (optional, default: false)
  stream: Use streaming for large files (optional, default: false)
  ```

- `agfs_upload` - Upload file or directory from local filesystem to AGFS
  ```
  local_path: Path to local file or directory
  remote_path: Destination path in AGFS
  recursive: Upload directories recursively (optional, default: false)
  stream: Use streaming for large files (optional, default: false)
  ```

- `agfs_download` - Download file or directory from AGFS to local filesystem
  ```
  remote_path: Path in AGFS
  local_path: Destination path on local filesystem
  recursive: Download directories recursively (optional, default: false)
  stream: Use streaming for large files (optional, default: false)
  ```

#### Directory Operations

- `agfs_ls` - List directory contents
  ```
  path: Directory path (optional, default: /)
  ```

- `agfs_mkdir` - Create directory
  ```
  path: Directory path to create
  mode: Permissions mode (optional, default: 755)
  ```

#### Search Operations

- `agfs_grep` - Search for pattern in files
  ```
  path: Path to search in
  pattern: Regular expression pattern
  recursive: Search recursively (optional, default: false)
  case_insensitive: Case-insensitive search (optional, default: false)
  ```

#### Plugin Management

- `agfs_mounts` - List all mounted plugins

- `agfs_mount` - Mount a plugin
  ```
  fstype: Filesystem type (e.g., 'sqlfs', 'memfs', 's3fs')
  path: Mount path
  config: Plugin configuration (optional)
  ```

- `agfs_unmount` - Unmount a plugin
  ```
  path: Mount path to unmount
  ```

#### Health Check

- `agfs_health` - Check AGFS server health status

#### Notification (QueueFS)

- `agfs_notify` - Send notification message via QueueFS
  ```
  queuefs_root: Root path of QueueFS (optional, default: /queuefs)
  to: Target queue name (receiver)
  from: Source queue name (sender)
  data: Message data to send
  ```
  Automatically creates sender and receiver queues if they don't exist.

## Example Usage with AI

Once configured, you can ask Claude (or other MCP-compatible AI assistants) to perform operations like:

- "List all files in the /data directory on AGFS"
- "Read the contents of /config/settings.json from AGFS"
- "Create a new directory called /logs/2024 in AGFS"
- "Copy /data/file.txt to /backup/file.txt in AGFS"
- "Upload my local file /tmp/report.pdf to /documents/report.pdf in AGFS"
- "Download /logs/app.log from AGFS to my local /tmp/app.log"
- "Copy the entire /data directory to /backup/data recursively in AGFS"
- "Search for 'error' in all files under /logs recursively"
- "Show me all mounted plugins in AGFS"
- "Mount a new memfs plugin at /tmp/cache"
- "Send a notification from 'service-a' to 'service-b' with message 'task completed'"

The AI will use the appropriate MCP tools to interact with your AGFS server.

## Environment Variables

- `AGFS_SERVER_URL`: AGFS server URL (default: `http://localhost:8080`)

## Requirements

- Python >= 3.10
- AGFS Server running and accessible
- pyagfs SDK
- mcp >= 0.9.0

## Development

### Setup

```bash
# Clone and install in development mode
git clone <repo>
cd agfs-mcp
uv pip install -e .
```

### Testing

Start a AGFS server first, then:

```bash
# Test the MCP server manually
echo '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | agfs-mcp
```

## Troubleshooting

- Verify AGFS before debugging MCP: `curl -sf http://localhost:8080/api/v1/health`.
- MCP is a stdio JSON-RPC process for an MCP client. It does not open an HTTP port or browser UI.
- If your MCP client cannot find `agfs-mcp`, use an absolute command path or a `uvx` config:
  ```json
  {
    "command": "uvx",
    "args": ["--from", "/path/to/agfs-mcp", "agfs-mcp"]
  }
  ```
- If tools fail with connection errors, set `AGFS_SERVER_URL=http://host:8080` in the MCP client environment.
- If `tools/list` works but AGFS operations fail, confirm plugin mounts with `curl -sf "http://localhost:8080/api/v1/directories?path=/"`.

For the full AGFS first-run matrix, see [../docs/first-run.md](../docs/first-run.md).

## Architecture

```
┌─────────────────┐
│   AI Assistant  │
│   (e.g. Claude) │
└────────┬────────┘
         │ MCP Protocol (JSON-RPC over stdio)
         │
┌────────▼────────┐
│ AGFS MCP Server │
│   (agfs-mcp)    │
└────────┬────────┘
         │ HTTP API
         │
┌────────▼────────┐
│   AGFS Server   │
│  (Plugin-based  │
│  File System)   │
└─────────────────┘
```

## License

See LICENSE file for details.

## Related Projects

- [AGFS](https://github.com/c4pt0r/agfs) - Plugin-based File System
- [pyagfs](../agfs-sdk/python) - AGFS Python SDK
- [Model Context Protocol](https://modelcontextprotocol.io/) - MCP Specification
