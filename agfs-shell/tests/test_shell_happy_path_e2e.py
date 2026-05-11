"""E2E happy/error path coverage for ``agfs-shell``.

Complements ``test_streaming_pipeline_e2e.py`` (which covers the
streaming-pipeline / readline rewrite) by exercising day-to-day
shell behaviours end-to-end: a simple command, a single-stage
pipeline against the live server, and the documented behaviour for
a command that doesn't exist.

Gated by ``AGFS_RUN_E2E=1``; honours ``AGFS_E2E_BASE_URL`` so the
shell/SDK lane harness can run one agfs-server for the whole lane.
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


REPO_ROOT = Path(__file__).resolve().parents[2]
SHELL_ROOT = REPO_ROOT / "agfs-shell"
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
    """Same fixture shape as the other lane tests — honours
    ``AGFS_E2E_BASE_URL`` for the shared lane-harness run, falls back
    to a per-fixture server otherwise."""
    shared = os.getenv("AGFS_E2E_BASE_URL")
    if shared:
        try:
            r = requests.get(f"{shared}/api/v1/health", timeout=2)
            r.raise_for_status()
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
        requests.post(
            f"{base_url}/api/v1/mount",
            json={"fstype": "memfs", "path": "/memfs", "config": {}},
            timeout=10,
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


def _shell(base_url: str, command: str, check: bool = True, timeout: int = 30):
    """Invoke ``uv run agfs-shell -c <command>`` against a live server."""
    _require_command("uv")
    env = os.environ.copy()
    env["AGFS_API_URL"] = base_url
    # ``--frozen`` so the lane never mutates ``agfs-shell/uv.lock``
    # under us — the shared E2E gate must leave tracked files clean.
    return subprocess.run(
        ["uv", "run", "--frozen", "agfs-shell", "-c", command],
        cwd=SHELL_ROOT,
        env=env,
        text=True,
        capture_output=True,
        timeout=timeout,
        check=check,
    )


@pytest.mark.e2e
@requires_e2e
def test_shell_echo_runs_and_returns_zero_against_real_server(live_agfs_server):
    """Smoke: a basic ``echo`` runs through the shell, prints to
    stdout, exits 0. This is the simplest user-visible flow and
    catches "shell can't even start when pointed at a real server"
    regressions."""
    result = _shell(live_agfs_server, "echo hello-shell-e2e")
    assert "hello-shell-e2e" in result.stdout, (
        f"echo did not surface its argument: {result.stdout!r} "
        f"stderr={result.stderr!r}"
    )
    assert result.returncode == 0


@pytest.mark.e2e
@requires_e2e
def test_shell_single_pipeline_against_real_server(live_agfs_server):
    """Single-stage pipeline through the shell against the live
    server. Different surface from ``test_streaming_pipeline_e2e.py``:
    that test stages a multi-MB file and stresses ``readline()``;
    this one exercises the small-input common case so a regression
    of the basic ``cmd | cmd`` plumbing surfaces here too."""
    result = _shell(live_agfs_server, "echo 'three lines\nand more\nthird' | wc -l")
    out = result.stdout.strip()
    # Some shell line-count formats include leading whitespace; take
    # the last token.
    last = out.split()[-1] if out else ""
    # echo's argument was a single-quoted string with two embedded
    # newlines plus the implicit trailing one — wc -l should count
    # three lines.
    assert last == "3", (
        f"unexpected wc -l output for embedded-newline echo: "
        f"stdout={out!r} stderr={result.stderr!r}"
    )


@pytest.mark.e2e
@requires_e2e
def test_shell_unknown_command_exits_nonzero_against_real_server(live_agfs_server):
    """A user-supplied command name that doesn't resolve to a builtin
    must exit non-zero and report something user-recognisable on
    stderr. Pins the shell's "command not found" surface."""
    result = _shell(live_agfs_server, "definitely_not_a_real_cmd_xyz", check=False)
    assert result.returncode != 0, (
        f"unknown command unexpectedly returned 0; stdout={result.stdout!r}"
    )
    combined = result.stdout + result.stderr
    # "No such command" / "command not found" / "not found" — any of
    # these wordings surfaces the user-recognisable failure mode.
    msg = combined.lower()
    assert any(token in msg for token in ("not found", "no such command")), (
        f"unknown command did not surface a recognisable error: "
        f"stdout={result.stdout!r} stderr={result.stderr!r}"
    )
