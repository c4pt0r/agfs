# AGFS E2E Gate

Every code or docs change that affects user-visible behavior must name its end-to-end coverage in the PR body.

## Required PR Checklist

Each PR must include an **E2E coverage** line with one of these forms:

- `Covered by scripts/e2e/run-core-e2e.sh` plus any focused e2e command added by the PR.
- `Covered by <specific e2e test/command>` when the core harness is not the relevant path.
- `No true e2e possible because <reason>; closest user-path verification: <command>` for narrow internal-only changes. This should be rare and cross-review may reject it when a realistic user path exists.

Unit tests are still required for detailed behavior, but they do not replace e2e coverage when the change has a user-visible flow.

## Core Harness

Run from the repository root:

```bash
scripts/e2e/run-core-e2e.sh
```

The harness starts a local `agfs-server` on `127.0.0.1:18080`, waits for `/api/v1/health`, checks `/api/v1/ready`, exercises a QueueFS enqueue/dequeue through the HTTP file API, runs a real local `agfs-shell` pipeline while the server is live, and builds the webapp from the committed lockfile.

Useful environment overrides:

- `AGFS_E2E_PORT=18081` changes the local server port.
- `AGFS_E2E_BASE_URL=http://host:port` changes the URL clients use.
- `AGFS_E2E_CONFIG=path/to/config.yaml` changes the server config path relative to `agfs-server`.
- `AGFS_E2E_SKIP_WEBAPP=1` skips the npm/webapp build smoke when Node/npm is unavailable. Do not use this in CI unless a separate webapp e2e job covers the same path.
- `AGFS_E2E_LOG_FILE=/tmp/agfs-e2e.log` changes the server log path.

## Ownership Lanes

- Backend/server e2e cases live under task #25.
- Shell/SDK/pipeline e2e cases live under task #26.
- Docs/webapp/first-run e2e cases live under task #27.

Lane tests may overlap the core harness when they need to exercise the same
user-visible behavior end to end. The core harness is a smoke screen, not a
deduplication target.

Cross-review should block a patch when a realistic user path exists but the author only provided unit tests.
