# Readiness and Mount Status

AGFS starts its HTTP server before asynchronous configured mounts finish. The
server now reports process health separately from mount readiness:

- `GET /api/v1/health` always returns HTTP 200 when the process can answer. Its
  JSON `status` is `healthy`, `starting`, or `degraded`.
- `GET /api/v1/ready` returns HTTP 200 only after all tracked configured mounts
  are mounted. It returns HTTP 503 while mounts are `pending` or when any mount
  has `failed`.
- `GET /api/v1/mounts` includes configured mounts that are still `pending` or
  have `failed`, not just mounts that successfully entered the mount tree.

Mount status fields:

- `status`: `pending`, `mounted`, or `failed`
- `error`: present for failed mounts
- `mounts` summary on health/readiness: total, pending, mounted, failed

The summary includes the always-on `/dev` mount as well as enabled configured
plugins, so `total` is the complete startup mount set tracked by the server.

This first pass does not make configured mounts block process startup. Operators
and automation should use `/api/v1/ready` when they need the configured mount set
to be usable before sending traffic.
