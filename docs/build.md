# Building AGFS from Source

Build commands for the two Go binaries:

- **agfs-server** — HTTP API server with built-in plugins (including the `agentfs` plugin)
- **agfs-fuse** — read-only FUSE client that mounts an AGFS server to a local directory

## Prerequisites

- Go 1.25+ (CI uses 1.25.1; `agfs-server/go.mod` declares `go 1.26.0` and the toolchain is fetched automatically when needed)
- No CGO required — both binaries build cleanly with `CGO_ENABLED=0`
- Both Go modules use `replace` directives pointing to the local `agfs-sdk/go`, so build from inside each component directory. No vendoring needed.

## agfs-server

```bash
cd agfs-server
CGO_ENABLED=0 go build -o build/agfs-server ./cmd/server
```

Or with make:

```bash
cd agfs-server
make build    # output: build/agfs-server
```

Run and verify:

```bash
./build/agfs-server -c config.example.yaml
curl http://localhost:8080/api/v1/health
```

Note: the `agentfs` plugin (`pkg/plugins/agentfs`) is compiled into the server binary — there is no separate build step for it.

## agfs-fuse

```bash
cd agfs-fuse
CGO_ENABLED=0 go build -o build/agfs-fuse ./cmd/agfs-fuse
```

Or with make:

```bash
cd agfs-fuse
make build    # output: build/agfs-fuse
```

Mount and verify:

```bash
./build/agfs-fuse --agfs-server-url http://localhost:8080 --mount /mnt/agfs
ls /mnt/agfs
```

For workloads that stream for a long time (e.g. LLM generation under the `agentfs` plugin), raise the streaming progress timeout, which defaults to 60 seconds and otherwise aborts reads with `EIO`:

```bash
./build/agfs-fuse --agfs-server-url http://localhost:8080 --mount /mnt/agfs --stream-timeout 5m
```

## Cross-compilation

Add `GOOS`/`GOARCH`, for example:

```bash
cd agfs-server && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o build/agfs-server-linux-arm64 ./cmd/server
```

## Docker build (no local Go toolchain)

```bash
docker run --rm -v "$(pwd)":/src -w /src golang:1.27 sh -c \
  "cd agfs-server && CGO_ENABLED=0 go build -o /tmp/agfs-server ./cmd/server && \
   cd ../agfs-fuse && CGO_ENABLED=0 go build -o /tmp/agfs-fuse ./cmd/agfs-fuse"
```

If the module cache is cold and the default proxy is unreachable, prepend `GOPROXY=https://goproxy.cn,direct` to the build commands.

Both variants were verified on 2026-11-15 (golang:1.27 container): both binaries compile cleanly, and the resulting server + FUSE client were run-tested end-to-end (FUSE mount, agentfs generation, TTL cache, skill hot-reload via the HTTP API).
