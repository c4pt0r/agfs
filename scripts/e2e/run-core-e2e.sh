#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
SERVER_DIR="$ROOT_DIR/agfs-server"
SHELL_DIR="$ROOT_DIR/agfs-shell"
WEBAPP_DIR="$SHELL_DIR/webapp"

HOST=${AGFS_E2E_HOST:-127.0.0.1}
PORT=${AGFS_E2E_PORT:-18080}
BASE_URL=${AGFS_E2E_BASE_URL:-http://$HOST:$PORT}
CONFIG=${AGFS_E2E_CONFIG:-config.example.yaml}
LOG_FILE=${AGFS_E2E_LOG_FILE:-$ROOT_DIR/.e2e-agfs-server.log}

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
  echo "[e2e] starting agfs-server at $BASE_URL"
  (
    cd "$SERVER_DIR"
    go run cmd/server/main.go -c "$CONFIG" -addr ":$PORT"
  ) >"$LOG_FILE" 2>&1 &
  SERVER_PID=$!

  if ! wait_for_http "$BASE_URL/api/v1/health"; then
    echo "[e2e] server did not become healthy; log follows" >&2
    sed -n '1,200p' "$LOG_FILE" >&2 || true
    exit 1
  fi
}

assert_contains() {
  local haystack=$1
  local needle=$2
  if [[ "$haystack" != *"$needle"* ]]; then
    echo "[e2e] expected output to contain: $needle" >&2
    echo "[e2e] actual output: $haystack" >&2
    exit 1
  fi
}

server_smoke() {
  echo "[e2e] server health/readiness/file API smoke"
  local health ready_code body path
  health=$(curl -fsS "$BASE_URL/api/v1/health")
  assert_contains "$health" '"status"'
  assert_contains "$health" '"ready"'

  ready_code=$(curl -sS -o /tmp/agfs-e2e-ready.json -w '%{http_code}' "$BASE_URL/api/v1/ready")
  if [[ "$ready_code" != "200" && "$ready_code" != "503" ]]; then
    echo "[e2e] /ready returned unexpected status: $ready_code" >&2
    cat /tmp/agfs-e2e-ready.json >&2 || true
    exit 1
  fi

  path="/queuefs/e2e-$(date +%s%N)"
  body="hello from e2e"
  curl -fsS -X PUT --data-binary "$body" "$BASE_URL/api/v1/files?path=$path/enqueue" >/dev/null
  local popped
  popped=$(curl -fsS "$BASE_URL/api/v1/files?path=$path/dequeue")
  assert_contains "$popped" "$body"
}

shell_smoke() {
  echo "[e2e] agfs-shell command smoke"
  (
    cd "$SHELL_DIR"
    uv sync --frozen >/dev/null
    AGFS_API_URL="$BASE_URL" uv run --frozen agfs-shell -c "echo shell-e2e | wc -l" | grep -q "1"
  )
}

webapp_smoke() {
  if [[ "${AGFS_E2E_SKIP_WEBAPP:-0}" == "1" ]]; then
    echo "[e2e] skipping webapp smoke because AGFS_E2E_SKIP_WEBAPP=1"
    return 0
  fi
  if ! command -v npm >/dev/null 2>&1; then
    echo "[e2e] npm not found; set AGFS_E2E_SKIP_WEBAPP=1 to skip webapp smoke intentionally" >&2
    exit 1
  fi

  echo "[e2e] webapp lockfile/build smoke"
  (
    cd "$SHELL_DIR"
    AGFS_RUN_E2E=1 AGFS_API_URL="$BASE_URL" uv run --frozen pytest tests/test_docs_webapp_e2e.py -q
  )
}

main() {
  start_server
  server_smoke
  shell_smoke
  webapp_smoke
  echo "[e2e] core e2e passed"
}

main "$@"
