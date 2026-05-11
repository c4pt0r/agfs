#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
SERVER_DIR="$ROOT_DIR/agfs-server"

HOST=${AGFS_BACKEND_E2E_HOST:-127.0.0.1}
PORT=${AGFS_BACKEND_E2E_PORT:-18082}
BASE_URL="http://$HOST:$PORT"
SECONDARY_PORT=$((PORT + 1))
SECONDARY_BASE_URL="http://$HOST:$SECONDARY_PORT"

TMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/agfs-backend-e2e.XXXXXX")
SERVER_BIN="$TMP_DIR/agfs-server"
SERVER_PID=""
SERVER_LOG=""

cleanup() {
  if [[ -n "$SERVER_PID" ]]; then
    kill "$SERVER_PID" >/dev/null 2>&1 || true
    wait "$SERVER_PID" >/dev/null 2>&1 || true
  fi
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

fail() {
  echo "[backend-e2e] $*" >&2
  if [[ -n "$SERVER_LOG" && -f "$SERVER_LOG" ]]; then
    echo "[backend-e2e] server log follows" >&2
    sed -n '1,220p' "$SERVER_LOG" >&2 || true
  fi
  exit 1
}

build_server() {
  echo "[backend-e2e] building agfs-server"
  (
    cd "$SERVER_DIR"
    go build -o "$SERVER_BIN" ./cmd/server
  )
}

start_server() {
  local config=$1
  local port=$2
  local base_url=$3
  SERVER_LOG="$TMP_DIR/server-$port.log"
  echo "[backend-e2e] starting agfs-server at $base_url"
  "$SERVER_BIN" -c "$config" -addr ":$port" >"$SERVER_LOG" 2>&1 &
  SERVER_PID=$!
  wait_for_http "$base_url/api/v1/health" || fail "server at $base_url did not expose /health"
}

stop_server() {
  if [[ -n "$SERVER_PID" ]]; then
    kill "$SERVER_PID" >/dev/null 2>&1 || true
    wait "$SERVER_PID" >/dev/null 2>&1 || true
    SERVER_PID=""
  fi
}

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

wait_for_ready() {
  local base_url=$1
  local attempts=${2:-80}
  local code
  for _ in $(seq 1 "$attempts"); do
    code=$(curl -sS -o "$TMP_DIR/ready.json" -w '%{http_code}' "$base_url/api/v1/ready")
    if [[ "$code" == "200" ]]; then
      return 0
    fi
    sleep 0.25
  done
  fail "server at $base_url did not become ready; last /ready response: $(cat "$TMP_DIR/ready.json" 2>/dev/null || true)"
}

wait_for_health_status() {
  local base_url=$1
  local expected=$2
  local attempts=${3:-80}
  local health
  for _ in $(seq 1 "$attempts"); do
    health=$(curl -fsS "$base_url/api/v1/health")
    if HEALTH_JSON="$health" EXPECTED_STATUS="$expected" python3 - <<'PY'
import json
import os
import sys

data = json.loads(os.environ["HEALTH_JSON"])
sys.exit(0 if data.get("status") == os.environ["EXPECTED_STATUS"] else 1)
PY
    then
      return 0
    fi
    sleep 0.25
  done
  fail "server at $base_url never reached health status '$expected'"
}

assert_contains() {
  local haystack=$1
  local needle=$2
  if [[ "$haystack" != *"$needle"* ]]; then
    fail "expected output to contain '$needle'; actual output: $haystack"
  fi
}

assert_health() {
  local health=$1
  local expected_status=$2
  local expected_ready=$3
  local expected_degraded=$4
  local expected_failed=$5
  HEALTH_JSON="$health" \
    EXPECTED_STATUS="$expected_status" \
    EXPECTED_READY="$expected_ready" \
    EXPECTED_DEGRADED="$expected_degraded" \
    EXPECTED_FAILED="$expected_failed" \
    python3 - <<'PY'
import json
import os
import sys

data = json.loads(os.environ["HEALTH_JSON"])

def as_bool(value):
    return value.lower() == "true"

expected = {
    "status": os.environ["EXPECTED_STATUS"],
    "ready": as_bool(os.environ["EXPECTED_READY"]),
    "degraded": as_bool(os.environ["EXPECTED_DEGRADED"]),
    "failed": int(os.environ["EXPECTED_FAILED"]),
}

checks = [
    (data.get("status") == expected["status"], f"status={data.get('status')!r}"),
    (data.get("ready") is expected["ready"], f"ready={data.get('ready')!r}"),
    (data.get("degraded") is expected["degraded"], f"degraded={data.get('degraded')!r}"),
    (data.get("mounts", {}).get("failed") == expected["failed"], f"failed={data.get('mounts', {}).get('failed')!r}"),
]
bad = [detail for ok, detail in checks if not ok]
if bad:
    print(f"unexpected health response: {data}; failed checks: {', '.join(bad)}", file=sys.stderr)
    sys.exit(1)
PY
}

assert_mount_status() {
  local mounts_json=$1
  local path=$2
  local status=$3
  MOUNTS_JSON="$mounts_json" EXPECTED_PATH="$path" EXPECTED_STATUS="$status" python3 - <<'PY'
import json
import os
import sys

data = json.loads(os.environ["MOUNTS_JSON"])
mounts = data.get("mounts", [])
matches = [m for m in mounts if m.get("path") == os.environ["EXPECTED_PATH"]]
if not matches:
    print(f"missing mount {os.environ['EXPECTED_PATH']}; mounts={mounts}", file=sys.stderr)
    sys.exit(1)
actual = matches[0].get("status")
if actual != os.environ["EXPECTED_STATUS"]:
    print(f"mount {os.environ['EXPECTED_PATH']} status={actual!r}, expected {os.environ['EXPECTED_STATUS']!r}", file=sys.stderr)
    sys.exit(1)
PY
}

http_code() {
  local method=$1
  local url=$2
  local out=$3
  shift 3
  curl -sS -o "$out" -w '%{http_code}' -X "$method" "$@" "$url"
}

write_healthy_config() {
  cat >"$TMP_DIR/healthy.yaml" <<YAML
server:
  address: ":18082"
  log_level: warn
  max_request_body_bytes: 32

plugins:
  memfs:
    enabled: true
    path: /memfs
    config: {}
  queuefs:
    - name: memory
      enabled: true
      path: /queuefs
      config: {}
    - name: sqlite
      enabled: true
      path: /queuefs-sqlite
      config:
        backend: sqlite
        db_path: $TMP_DIR/queuefs-sqlite-e2e.db
  serverinfofs:
    enabled: true
    path: /serverinfo
    config:
      version: backend-e2e
  hellofs:
    enabled: true
    path: /hello
    config: {}
YAML
}

write_degraded_config() {
  cat >"$TMP_DIR/degraded.yaml" <<YAML
server:
  address: ":$SECONDARY_PORT"
  log_level: warn
  max_request_body_bytes: 32

plugins:
  memfs:
    enabled: true
    path: /memfs
    config: {}
  localfs:
    enabled: true
    path: /badlocal
    config:
      local_dir: $TMP_DIR/does-not-exist
YAML
}

healthy_server_e2e() {
  write_healthy_config
  start_server "$TMP_DIR/healthy.yaml" "$PORT" "$BASE_URL"
  wait_for_ready "$BASE_URL"

  echo "[backend-e2e] healthy readiness and mount status"
  local health mounts
  health=$(curl -fsS "$BASE_URL/api/v1/health")
  assert_health "$health" "healthy" "true" "false" "0"

  mounts=$(curl -fsS "$BASE_URL/api/v1/mounts")
  assert_mount_status "$mounts" "/dev" "mounted"
  assert_mount_status "$mounts" "/memfs" "mounted"
  assert_mount_status "$mounts" "/queuefs" "mounted"
  assert_mount_status "$mounts" "/queuefs-sqlite" "mounted"
  assert_mount_status "$mounts" "/serverinfo" "mounted"
  assert_mount_status "$mounts" "/hello" "mounted"

  echo "[backend-e2e] core HTTP file API"
  local code response body path
  path="/memfs/e2e/file.txt"
  curl -fsS -X POST "$BASE_URL/api/v1/directories?path=/memfs/e2e" >/dev/null
  body="backend-e2e-ok"
  curl -fsS -X PUT --data-binary "$body" "$BASE_URL/api/v1/files?path=$path" >/dev/null
  response=$(curl -fsS "$BASE_URL/api/v1/files?path=$path")
  [[ "$response" == "$body" ]] || fail "file API read returned '$response', expected '$body'"
  response=$(curl -fsS "$BASE_URL/api/v1/stat?path=$path")
  assert_contains "$response" '"name":"file.txt"'
  code=$(http_code GET "$BASE_URL/api/v1/files" "$TMP_DIR/missing-path.json")
  [[ "$code" == "400" ]] || fail "file API without path should return 400, got $code with $(cat "$TMP_DIR/missing-path.json")"

  echo "[backend-e2e] HTTP body-limit 413 behavior"
  local too_large="012345678901234567890123456789012"
  code=$(http_code PUT "$BASE_URL/api/v1/files?path=/memfs/e2e/too-large.txt" "$TMP_DIR/too-large.json" --data-binary "$too_large")
  [[ "$code" == "413" ]] || fail "oversized write should return 413, got $code with $(cat "$TMP_DIR/too-large.json")"
  assert_contains "$(cat "$TMP_DIR/too-large.json")" "maximum size of 32 bytes"
  code=$(http_code GET "$BASE_URL/api/v1/files?path=/memfs/e2e/too-large.txt" "$TMP_DIR/too-large-read.json")
  [[ "$code" != "200" ]] || fail "oversized write should not create target file"

  echo "[backend-e2e] mounted plugin smoke paths"
  response=$(curl -fsS "$BASE_URL/api/v1/files?path=/serverinfo/version")
  assert_contains "$response" "backend-e2e"
  response=$(curl -fsS "$BASE_URL/api/v1/files?path=/hello/hello")
  assert_contains "$response" "Hello, World!"
  local queue="/queuefs/e2e-$(date +%s%N)"
  curl -fsS -X PUT --data-binary "queued-message" "$BASE_URL/api/v1/files?path=$queue/enqueue" >/dev/null
  response=$(curl -fsS "$BASE_URL/api/v1/files?path=$queue/dequeue")
  assert_contains "$response" "queued-message"

  echo "[backend-e2e] SQLite QueueFS concurrent dequeue smoke"
  local sqlite_queue="/queuefs-sqlite/e2e-$(date +%s%N)"
  curl -fsS -X POST "$BASE_URL/api/v1/directories?path=$sqlite_queue" >/dev/null
  for i in $(seq 0 7); do
    curl -fsS -X PUT --data-binary "sqlite-message-$i" "$BASE_URL/api/v1/files?path=$sqlite_queue/enqueue" >/dev/null
  done
  BASE_URL="$BASE_URL" QUEUE_PATH="$sqlite_queue" python3 - <<'PY'
import concurrent.futures
import json
import os
import urllib.parse
import urllib.request

base = os.environ["BASE_URL"]
queue = os.environ["QUEUE_PATH"]

def dequeue(_):
    query = urllib.parse.urlencode({"path": f"{queue}/dequeue"})
    with urllib.request.urlopen(f"{base}/api/v1/files?{query}", timeout=10) as response:
        return response.read().decode()

with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
    bodies = list(pool.map(dequeue, range(8)))

messages = [json.loads(body) for body in bodies]
data = [msg.get("data") for msg in messages]
expected = {f"sqlite-message-{i}" for i in range(8)}
if set(data) != expected or len(data) != len(set(data)):
    raise SystemExit(f"unexpected SQLite QueueFS dequeue results: {data}")
PY
  response=$(curl -fsS "$BASE_URL/api/v1/files?path=$sqlite_queue/dequeue")
  [[ "$response" == "{}" ]] || fail "SQLite QueueFS should be empty after concurrent dequeue, got '$response'"

  echo "[backend-e2e] unmount invalidates open handles"
  local handle_mount="/memfs"
  local handle_path="$handle_mount/e2e/file.txt"
  response=$(curl -fsS -X POST "$BASE_URL/api/v1/handles/open?path=$handle_path&flags=2")
  local handle_id
  handle_id=$(HANDLE_OPEN_JSON="$response" python3 - <<'PY'
import json
import os

print(json.loads(os.environ["HANDLE_OPEN_JSON"])["handle_id"])
PY
)
  response=$(curl -fsS "$BASE_URL/api/v1/handles/$handle_id/read?size=7")
  [[ "$response" == "backend" ]] || fail "open handle read returned '$response', expected 'backend'"
  curl -fsS -X POST -H "Content-Type: application/json" \
    --data "{\"path\":\"$handle_mount\"}" \
    "$BASE_URL/api/v1/unmount" >/dev/null
  code=$(http_code GET "$BASE_URL/api/v1/handles/$handle_id/read?size=7" "$TMP_DIR/handle-read-after-unmount.json")
  [[ "$code" == "404" ]] || fail "handle read after unmount should return 404, got $code with $(cat "$TMP_DIR/handle-read-after-unmount.json")"
  assert_contains "$(cat "$TMP_DIR/handle-read-after-unmount.json")" "not found"
  code=$(http_code DELETE "$BASE_URL/api/v1/handles/$handle_id" "$TMP_DIR/handle-close-after-unmount.json")
  [[ "$code" == "404" ]] || fail "handle close after unmount should return 404, got $code with $(cat "$TMP_DIR/handle-close-after-unmount.json")"

  stop_server
}

degraded_server_e2e() {
  write_degraded_config
  start_server "$TMP_DIR/degraded.yaml" "$SECONDARY_PORT" "$SECONDARY_BASE_URL"
  wait_for_health_status "$SECONDARY_BASE_URL" "degraded"

  echo "[backend-e2e] degraded readiness and failed mount visibility"
  local code health mounts
  health=$(curl -fsS "$SECONDARY_BASE_URL/api/v1/health")
  assert_health "$health" "degraded" "false" "true" "1"
  code=$(http_code GET "$SECONDARY_BASE_URL/api/v1/ready" "$TMP_DIR/degraded-ready.json")
  [[ "$code" == "503" ]] || fail "degraded /ready should return 503, got $code with $(cat "$TMP_DIR/degraded-ready.json")"
  mounts=$(curl -fsS "$SECONDARY_BASE_URL/api/v1/mounts")
  assert_mount_status "$mounts" "/badlocal" "failed"
  assert_contains "$mounts" "base path does not exist"

  stop_server
}

main() {
  build_server
  healthy_server_e2e
  degraded_server_e2e
  echo "[backend-e2e] backend e2e passed"
}

main "$@"
