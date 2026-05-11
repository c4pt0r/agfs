# HTTP Body Limits and Large-Write Strategy

AGFS server caps write and JSON request bodies with `server.max_request_body_bytes`.
When the value is unset or non-positive, the server uses the default `67108864`
bytes (64 MiB). Over-limit requests fail before handler-level buffering with
HTTP `413 Request Entity Too Large`.

## Covered in the First Pass

- Raw file writes: `PUT /api/v1/files` and raw `POST /api/v1/write`.
- JSON write alias: `POST /api/v1/write` with `application/json`.
- Stateful handle writes: `PUT /api/v1/handles/{id}/write`.
- JSON control endpoints in the core and plugin handlers: rename, chmod,
  digest, symlink, grep, mount, unmount, plugin load, and plugin unload.

The first pass intentionally keeps the existing filesystem `Write(path, []byte,
...)` contract. This bounds server memory risk for request ingestion without
changing plugin write semantics in the same patch.

## Already Streaming

- File reads can stream through `GET /api/v1/files?stream=true` when the mounted
  filesystem implements `filesystem.Streamer`.
- Handle streams use `GET /api/v1/handles/{id}/stream`.
- Digest calculation uses `Open` and fixed-size read buffers instead of loading
  full files into handler memory.

## Follow-Up Architecture Work

- Add a write-streaming API that uses `filesystem.OpenWrite` plus `io.Copy`
  where backends support it. Keep the current `Write([]byte)` path as the
  compatibility fallback.
- Teach storage plugins with native streaming or multipart support, especially
  S3FS, to avoid buffering whole objects before upload.
- Audit plugins that transform full file contents, especially VectorFS indexing,
  so large inputs are chunked or rejected with explicit limits.
- Add size limits or streaming downloads for external plugin loading from HTTP
  and AGFS paths.
- Document client guidance for chunked uploads once the server exposes a stable
  write-streaming endpoint.
