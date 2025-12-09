# <img src="./assets/logo-white.png" alt="AGFS Logo" height="40" style="vertical-align: middle;"/>

[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![Daily Build](https://github.com/c4pt0r/agfs/actions/workflows/daily-build.yml/badge.svg)](https://github.com/c4pt0r/agfs/actions/workflows/daily-build.yml)

Aggregated File System (or Agent FS), originally known as pfs (Plugin File System)

"Everything is a file."
A tribute to Plan9, but in RESTful APIs.

The motivation for this project is that I wanted to find a unified way to coordinate and orchestrate multiple AI agents in a distributed environment. 
In the end, I realized that bash + filesystem is actually the best solution, e.g. some interesting patterns like `task_loop`:

[https://github.com/c4pt0r/agfs/blob/master/agfs-mcp/demos/task_loop.py](https://github.com/c4pt0r/agfs/blob/master/agfs-mcp/demos/task_loop.py)


## Installation

### Quick Install agfs-{server, shell} (Daily Build)

```bash
curl -fsSL https://raw.githubusercontent.com/c4pt0r/agfs/master/install.sh | sh
```


### Docker (agfs-server)

```bash
$ docker pull c4pt0r/agfs-server:latest
```

```plain
$ agfs
     __  __ __
 /\ / _ |_ (_
/--\\__)|  __)

agfs-shell v1.1.0
Connected to AGFS server at http://localhost:8080
Type 'help' for help, Ctrl+D or 'exit' to quit

agfs:/> ls -l
drwxr-xr-x        0 2025-11-21 13:42:23 queuefs_mem/
drwxr-xr-x        0 2025-11-21 13:42:23 kvfs/
drwxr-xr-x        0 2025-11-21 13:42:23 hellofs/
drwxr-xr-x        0 2025-11-21 13:42:23 local/
drwxr-xr-x        0 2025-11-21 13:42:23 sqlfs/
drwxr-xr-x        0 2025-11-21 13:42:23 s3fs/
drwxr-xr-x        0 2025-11-21 13:42:23 sqlfs2/
drwxr-xr-x        0 2025-11-21 13:42:23 serverinfofs/
drwxr-xr-x        0 2025-11-21 13:42:23 heartbeatfs/
drwxr-xr-x        0 2025-11-21 13:42:23 memfs/
drwxr-xr-x        0 2025-11-21 13:42:23 streamfs/


# BROWSING YOUR S3 BUCKET
agfs:/> agfs:/s3fs/aws> ls -l
drw-r--r--        0 2025-11-21 13:42:51 dongxu/
-rw-r--r--       12 2025-11-17 23:55:26 hello.txt
-rw-r--r--    96192 2025-11-17 23:47:31 hellofs-wasm.wasm


# SQLFS

agfs:/sqlfs2/tidb/log/logs> cat schema
CREATE TABLE `logs` (
  `id` int(10) unsigned NOT NULL AUTO_INCREMENT,
  `name` text DEFAULT NULL,
  `content` longblob DEFAULT NULL,
  `created_at` timestamp DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`) /*T![clustered_index] CLUSTERED */
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin AUTO_INCREMENT=90001
agfs:/sqlfs2/tidb/log/logs>

agfs:/> cat << EOF > /sqlfs2/tidb/log/logs/query
SELECT *
FROM logs
LIMIT 1;
EOF
[
  {
    "content": "world",
    "created_at": null,
    "id": 10,
    "name": "hello"
  }
]


# QUEUEFS

agfs:/queuefs_mem/queue> ls
enqueue
dequeue
peek
size
clear
agfs:/queuefs_mem/queue> echo hello > enqueue
019aa869-1a20-7ca6-a77a-b081e24c0593
agfs:/queuefs_mem/queue> cat dequeue
{"id":"019aa869-1a20-7ca6-a77a-b081e24c0593","data":"hello\n","timestamp":"2025-11-21T13:54:11.616801-08:00"}
agfs:/queuefs_mem/queue>


# HEARTBEATFS

agfs:/heartbeatfs> mkdir agent-1
agfs:/heartbeatfs> cd agent-1
agfs:/heartbeatfs/agent-1> ls
keepalive
ctl

agfs:/heartbeatfs/agent-1> touch keepalive
agfs:/heartbeatfs/agent-1>

agfs:/heartbeatfs/agent-1> cat ctl
last_heartbeat_ts: 2025-11-21T13:55:45-08:00
expire_ts: 2025-11-21T13:56:15-08:00
timeout: 30
status: alive

agfs:/heartbeatfs/agent-1> sleep 30

# Cleanup by FS
agfs:/heartbeatfs/agent-1> ls
ls: /heartbeatfs/agent-1: No such file or directory


# Upload/download local file, you can also use cp to copy files between FS!

agfs:/local> cp local:/tmp/test_input.txt ./newfile
local:/tmp/test_input.txt -> /local/newfile

// recursive upload dir
agfs:/> cp -r local:./agfscli ./local/
Uploaded 2098 bytes to /local/agfscli/__pycache__/decorators.cpython-312.pyc
Uploaded 26197 bytes to /local/agfscli/__pycache__/cli.cpython-312.pyc
Uploaded 25802 bytes to /local/agfscli/__pycache__/cli.cpython-313.pyc
Uploaded 88764 bytes to /local/agfscli/__pycache__/commands.cpython-312.pyc
Uploaded 51420 bytes to /local/agfscli/__pycache__/cli_commands.cpython-312.pyc
Uploaded 88244 bytes to /local/agfscli/__pycache__/commands.cpython-313.pyc
Uploaded 611 bytes to /local/agfscli/__pycache__/version.cpython-312.pyc
Uploaded 237 bytes to /local/agfscli/__pycache__/__init__.cpython-312.pyc
agfs:/>


```


See more details in:
- [agfs-shell/README.md](https://github.com/c4pt0r/agfs/blob/master/agfs-shell/README.md)
- [agfs-server/README.md](https://github.com/c4pt0r/agfs/blob/master/agfs-server/README.md)











