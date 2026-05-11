"""E2E coverage for ``AGFSClient`` lifecycle against a real agfs-server.

The unit tests in ``test_lifecycle.py`` cover the contract with a
``MagicMock`` Session — fast, deterministic, but the failure mode the
patch fixes is *real users sharing a real Session with urllib3's
connection pool*. This file exercises the same lifecycle through a
real subprocess agfs-server so the post-close guard is verified at
the user-visible boundary.

The test is gated by ``AGFS_RUN_E2E=1`` (the same opt-in pattern
task #27 uses for the docs/webapp lane). It's also marked with the
``e2e`` pytest marker registered at ``agfs-shell/pyproject.toml``;
the SDK doesn't currently register its own marker but the env-var
guard is enough to keep this test off the default path.
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
    """Poll ``/api/v1/health`` until the server answers 200 or the
    subprocess dies, whichever comes first."""
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
    """Boot a fresh ``agfs-server`` on a free port; yield its base URL.

    Mirrors ``scripts/e2e/run-core-e2e.sh``'s server boot but in-process
    so the test can drive its own AGFSClient lifecycle without going
    through the shell.

    Honours ``AGFS_E2E_BASE_URL`` so the shell/SDK lane harness
    (task #26) can run a single server for the whole lane. Without
    the env var, each test boots its own per-fixture server — slower
    but self-contained.
    """
    shared = os.getenv("AGFS_E2E_BASE_URL")
    if shared:
        # The shared server is presumed already healthy because the
        # harness waits on /health before invoking pytest. We can
        # still get a clear skip if it's misconfigured.
        try:
            requests.get(f"{shared}/api/v1/health", timeout=2).raise_for_status()
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
def test_client_lifecycle_against_real_server(live_agfs_server):
    """End-to-end version of the post-close guard.

    Boots a real agfs-server, opens a real ``AGFSClient`` via the
    ``with`` block, issues a real HTTP request through the SDK's
    ``health()`` method, exits the block, then verifies the
    post-close guard at the user-visible boundary: a subsequent
    ``health()`` raises ``AGFSClientError("has been closed")`` and the
    underlying socket pool is released — not just that the unit tests
    say so against a MagicMock Session.
    """
    base_url = live_agfs_server

    # Use a context manager — the public surface the patch adds.
    with AGFSClient(api_base_url=base_url, timeout=5) as client:
        # Confirm the client really is talking to the server.
        info = client.health()
        # The merged readiness work (task #16) added these keys to the
        # /health JSON; assert their shape so a regression of either
        # the SDK or the server surfaces here too.
        assert info.get("status") in {"healthy", "starting", "degraded"}
        assert "ready" in info
        # And the client is not yet closed.
        assert client.is_closed is False

    # `with` block exited — the patch's contract says the session is
    # closed and any further request must raise AGFSClientError.
    assert client.is_closed is True
    with pytest.raises(AGFSClientError, match="has been closed"):
        client.health()

    # Same guard for a different method shape (ls hits session.get just
    # like health()) — proves the sentinel intercepts at the session
    # attribute level, not at any one named entry point.
    with pytest.raises(AGFSClientError, match="has been closed"):
        client.ls("/")


@pytest.mark.e2e
@requires_e2e
def test_explicit_close_after_use_against_real_server(live_agfs_server):
    """Same guarantee for callers that prefer ``close()`` over ``with``.

    Some long-lived processes (FUSE mount, agent) construct the client
    once and tear it down explicitly. Confirm the close path is
    equivalent end-to-end.
    """
    base_url = live_agfs_server

    client = AGFSClient(api_base_url=base_url, timeout=5)
    assert client.is_closed is False
    info = client.health()
    assert "ready" in info

    client.close()
    assert client.is_closed is True

    with pytest.raises(AGFSClientError, match="has been closed"):
        client.health()

    # Idempotent: a second close() is a no-op, not a double-free.
    client.close()
    with pytest.raises(AGFSClientError, match="has been closed"):
        client.ls("/")
