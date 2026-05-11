#!/usr/bin/env bash
# Shell / SDK / pipeline / Python-client-lifecycle E2E lane (task #26).
#
# Boots one `agfs-server` for the whole lane, exports `AGFS_E2E_BASE_URL`
# so the pytest fixtures reuse it instead of booting a server per test,
# then runs the opt-in `@pytest.mark.e2e` tests in `agfs-shell/` and
# `agfs-sdk/python/`. Covers:
#
#   * agfs-shell pipeline correctness + readline performance
#     (test_streaming_pipeline_e2e.py — landed via task #20)
#   * agfs-shell happy path + error path
#     (test_shell_happy_path_e2e.py — added by this task)
#   * Python SDK lifecycle / post-close guard
#     (test_lifecycle_e2e.py — landed via task #21)
#   * Python SDK ls/write/read/stat/rm happy path + error path
#     (test_sdk_happy_path_e2e.py — added by this task)
#
# Useful environment overrides:
#
#   AGFS_SHELL_SDK_E2E_PORT=18084  change the server port
#   AGFS_SHELL_SDK_E2E_HOST=127.0.0.1  change the bind host
#   AGFS_SHELL_SDK_E2E_LOG=/tmp/agfs-shell-sdk-e2e.log  change log path

set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
SERVER_DIR="$ROOT_DIR/agfs-server"
SHELL_DIR="$ROOT_DIR/agfs-shell"
SDK_DIR="$ROOT_DIR/agfs-sdk/python"

HOST=${AGFS_SHELL_SDK_E2E_HOST:-127.0.0.1}
PORT=${AGFS_SHELL_SDK_E2E_PORT:-18084}
BASE_URL="http://$HOST:$PORT"
LOG_FILE=${AGFS_SHELL_SDK_E2E_LOG:-$ROOT_DIR/.e2e-shell-sdk-server.log}

SERVER_PID=""
cleanup() {
  if [[ -n "$SERVER_PID" ]]; then
    kill "$SERVER_PID" >/dev/null 2>&1 || true
    wait "$SERVER_PID" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

wait_for_http() {
  local url=$1
  local attempts=${2:-80}
  for _ in $(seq 1 "$attempts"); do
    if curl -fsS "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.25
  done
  return 1
}

start_server() {
  echo "[shell-sdk-e2e] starting agfs-server at $BASE_URL"
  (
    cd "$SERVER_DIR"
    go run cmd/server/main.go -c config.example.yaml -addr ":$PORT"
  ) >"$LOG_FILE" 2>&1 &
  SERVER_PID=$!

  if ! wait_for_http "$BASE_URL/api/v1/health"; then
    echo "[shell-sdk-e2e] server did not become healthy; log follows" >&2
    sed -n '1,200p' "$LOG_FILE" >&2 || true
    exit 1
  fi

  # Pre-mount memfs so the pytest fixtures don't have to retry; the
  # mount call is idempotent on our side — 409 (already mounted) is
  # fine, so we silence both the curl exit code and the stderr noise.
  curl -sS -X POST -H 'Content-Type: application/json' \
    -d '{"fstype":"memfs","path":"/memfs","config":{}}' \
    "$BASE_URL/api/v1/mount" >/dev/null 2>&1 || true
}

main() {
  start_server

  # Shell-side lane: streaming pipeline (task #20) + happy/error
  # path (this task). pytest lives in agfs-shell's
  # ``[dependency-groups.dev]``, which ``uv run`` installs by
  # default. ``--frozen`` keeps ``uv.lock`` immutable — the gate
  # has to leave the checkout clean.
  echo "[shell-sdk-e2e] shell e2e"
  (
    cd "$SHELL_DIR"
    uv sync --frozen >/dev/null
    AGFS_RUN_E2E=1 AGFS_E2E_BASE_URL="$BASE_URL" \
      uv run --frozen pytest -q \
        tests/test_streaming_pipeline_e2e.py \
        tests/test_shell_happy_path_e2e.py
  )

  # SDK-side lane: lifecycle (task #21) + ls/write/read/stat happy
  # path + error path (this task). pytest lives under the SDK's
  # ``[project.optional-dependencies.dev]`` so ``uv run`` needs
  # ``--extra dev`` to install it. ``--frozen`` again pins
  # ``uv.lock`` so the harness is deterministic.
  echo "[shell-sdk-e2e] sdk e2e"
  (
    cd "$SDK_DIR"
    uv sync --frozen --extra dev >/dev/null
    AGFS_RUN_E2E=1 AGFS_E2E_BASE_URL="$BASE_URL" \
      uv run --frozen --extra dev pytest -q \
        tests/test_lifecycle_e2e.py \
        tests/test_sdk_happy_path_e2e.py
  )

  echo "[shell-sdk-e2e] shell/SDK e2e passed"
}

main "$@"
