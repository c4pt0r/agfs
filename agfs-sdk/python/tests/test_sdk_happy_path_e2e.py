"""E2E coverage for the Python SDK's happy and error paths.

Complements ``test_lifecycle_e2e.py`` (which covers ``close()`` /
``__enter__`` / ``__exit__`` against a real server) by exercising the
day-to-day SDK operations end-to-end: ``ls``, ``write``, ``read``,
``stat``, ``rm``, plus one explicit "not-found" error path.

Gated by ``AGFS_RUN_E2E=1``; honours ``AGFS_E2E_BASE_URL`` so the
shell/SDK lane harness can run one agfs-server for the whole lane
instead of one per test file. When the env var is absent, the test
boots its own server.
"""

from __future__ import annotations

import os
import shutil
import socket
import subprocess
import time
from pathlib import Path

import pytest
import requests

from pyagfs import AGFSClient
from pyagfs.exceptions import AGFSClientError


REPO_ROOT = Path(__file__).resolve().parents[3]
SERVER_DIR = REPO_ROOT / "agfs-server"

RUN_E2E = os.getenv("AGFS_RUN_E2E") == "1"
requires_e2e = pytest.mark.skipif(
    not RUN_E2E,
    reason="set AGFS_RUN_E2E=1 to run subprocess-based E2E checks",
)


def _require_command(name: str) -> None:
    if shutil.which(name) is None:
        pytest.skip(f"{name} is required for this E2E test")


def _free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def _wait_for_health(base_url: str, proc: subprocess.Popen, timeout: float = 20) -> None:
    deadline = time.monotonic() + timeout
    last_error: Exception | None = None
    while time.monotonic() < deadline:
        if proc.poll() is not None:
            stdout, stderr = proc.communicate()
            raise AssertionError(
                f"agfs-server exited early with {proc.returncode}\n"
                f"stdout:\n{stdout}\nstderr:\n{stderr}"
            )
        try:
            r = requests.get(f"{base_url}/api/v1/health", timeout=1)
            if r.status_code == 200:
                return
        except requests.exceptions.RequestException as exc:
            last_error = exc
        time.sleep(0.25)
    raise AssertionError(f"agfs-server did not become healthy: {last_error}")


@pytest.fixture
def live_agfs_server():
    """Yield a base URL for a live agfs-server with memfs mounted.

    Honours ``AGFS_E2E_BASE_URL`` so the lane harness can amortise
    the server boot across every e2e test in the run. Without it,
    boots a per-test server (slower but self-contained).
    """
    shared = os.getenv("AGFS_E2E_BASE_URL")
    if shared:
        # Make sure memfs is mounted for the shared server too.
        try:
            requests.post(
                f"{shared}/api/v1/mount",
                json={"fstype": "memfs", "path": "/memfs", "config": {}},
                timeout=10,
            )
        except requests.exceptions.RequestException:
            pytest.skip(f"AGFS_E2E_BASE_URL={shared} is not reachable")
        yield shared
        return

    _require_command("go")
    port = _free_port()
    base_url = f"http://127.0.0.1:{port}"
    proc = subprocess.Popen(
        ["go", "run", "cmd/server/main.go", "-c", "config.example.yaml", "-addr", f":{port}"],
        cwd=SERVER_DIR,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    try:
        _wait_for_health(base_url, proc)
        mount = requests.post(
            f"{base_url}/api/v1/mount",
            json={"fstype": "memfs", "path": "/memfs", "config": {}},
            timeout=10,
        )
        assert mount.status_code in (200, 201, 409), (
            f"mount /memfs failed: {mount.status_code} {mount.text}"
        )
        yield base_url
    finally:
        if proc.poll() is None:
            proc.terminate()
            try:
                proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait(timeout=10)


@pytest.mark.e2e
@requires_e2e
def test_sdk_write_read_round_trip_against_real_server(live_agfs_server):
    """Day-1 SDK happy path: write a file, read it back, stat it.

    Exercises ``AGFSClient.write`` / ``read`` / ``stat`` against a
    real agfs-server. Uses the ``with`` lifecycle from task #21 so
    a regression of the lifecycle contract would also surface here.
    """
    payload = b"sdk-e2e-round-trip payload\nline two\n"
    path = f"/memfs/sdk-e2e-roundtrip-{os.getpid()}"

    with AGFSClient(api_base_url=live_agfs_server, timeout=10) as client:
        client.write(path, payload)

        # Read it back; bytes round-trip exactly.
        got = client.read(path)
        assert got == payload, f"round-trip mismatch: {got!r} vs {payload!r}"

        info = client.stat(path)
        assert info.get("size") == len(payload), (
            f"stat returned size {info.get('size')}, expected {len(payload)}"
        )
        # Stat is for a regular file, not a directory.
        assert info.get("isDir") is False, f"stat unexpectedly says isDir: {info}"

        # Clean up so repeated runs stay tidy.
        client.rm(path)


@pytest.mark.e2e
@requires_e2e
def test_sdk_ls_lists_directory_against_real_server(live_agfs_server):
    """``ls('/')`` returns the configured mounts as directory entries.

    /dev is mounted by default and /memfs is mounted by the fixture,
    so both must appear in the listing. Exercises the JSON-decoding
    side of ``AGFSClient.ls`` against the real server response shape.
    """
    with AGFSClient(api_base_url=live_agfs_server, timeout=10) as client:
        entries = client.ls("/")

    names = {e.get("name") for e in entries}
    # Must include both the default /dev and the fixture-mounted /memfs.
    assert "dev" in names, f"expected /dev in root listing: {names}"
    assert "memfs" in names, f"expected /memfs in root listing: {names}"


@pytest.mark.e2e
@requires_e2e
def test_sdk_read_nonexistent_path_raises_against_real_server(live_agfs_server):
    """Error path: ``read`` on a missing path raises a typed
    ``AGFSClientError`` rather than returning silently empty.

    Pins the SDK's error-to-exception translation through the real
    HTTP layer — guards against a regression where a future
    ``_handle_request_error`` refactor swallows 404s.
    """
    missing = f"/memfs/sdk-e2e-no-such-file-{os.getpid()}"

    with AGFSClient(api_base_url=live_agfs_server, timeout=10) as client:
        with pytest.raises(AGFSClientError):
            client.read(missing)
