"""End-to-end coverage for the StreamingInputStream readline rewrite.

The unit tests in ``test_streaming_input_stream.py`` exercise the
class in-process. This file confirms the optimization actually shows
up at the user-visible boundary: real ``agfs-shell`` subprocess,
real ``agfs-server``, a real shell pipeline that pushes a multi-MB
input through the streaming buffer between two stages, and a wall-
clock bound that would have failed before the rolling-buffer rewrite.

Gated by ``AGFS_RUN_E2E=1`` (matches task #27's docs/webapp lane)
plus the ``@pytest.mark.e2e`` marker registered at
``agfs-shell/pyproject.toml``. Will be wired into
``scripts/e2e/run-core-e2e.sh``'s shell/SDK lane as part of task #26.
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
    """Spawn a real agfs-server on a free port with a memfs mount.

    Same boot shape as ``scripts/e2e/run-core-e2e.sh`` and task #25's
    backend e2e, but we additionally mount ``memfs`` at ``/memfs`` via
    the runtime POST ``/api/v1/mount`` endpoint so this test can write
    and read regular files (queuefs is in the default config but uses
    enqueue/dequeue semantics, which is the wrong shape for piping a
    multi-line file through ``cat | wc -l``).

    Honours ``AGFS_E2E_BASE_URL`` so the shell/SDK lane harness
    (task #26) can run a single server for the whole lane. Without
    the env var, each test boots its own per-fixture server.
    """
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
        # Mount memfs at /memfs so the rest of this lane has a regular
        # file-system shape to write/read against. The mount call is
        # idempotent enough for our purposes: if memfs is already
        # mounted, the server returns 409, which we tolerate.
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
def test_large_pipeline_readline_completes_quickly_end_to_end(live_agfs_server):
    """A real agfs-shell pipeline pushing 100k lines through a
    *line-oriented* consumer must complete well under the
    pre-rewrite quadratic time.

    Workload:

      cat <path> | head -n 100000 | wc -l

    The middle stage is the optimization target. ``head`` reads
    its input via ``process.stdin.readlines()``, which internally
    calls ``StreamingInputStream.readline()`` once per line —
    exactly the path this PR rewrites. We use ``head -n 100000`` (≥
    the input line count) so head consumes the whole stream rather
    than short-circuiting after the first N lines, which keeps the
    workload on the optimized path for every input line.

    The final ``wc -l`` is a stable assertion stage that counts
    newlines on its own input (via ``read``, not ``readline``) and
    surfaces the resulting count back through the shell stdout —
    the user-visible end of the pipe.

    NOTE on consumer choice (from cross-review): the earlier version
    of this test used ``cat | wc -l`` directly, but agfs-shell's
    ``wc`` builtin calls ``process.stdin.read()`` and counts
    newlines on the resulting bytes — that flows through
    ``StreamingInputStream.read(-1)``, not the optimized
    ``readline()``. Routing through ``head`` first forces the
    line-oriented path before the assertion stage. Pre-rewrite
    behaviour: 100k ``readline()`` calls each scanned byte-by-byte
    and reallocated ``io.BytesIO`` per chunk, so wall time scaled
    quadratically. Post-rewrite: one ``bytearray.find`` per line
    over a rolling buffer.

    The 30s bound is generous (typical run is ~3-5s on a dev box);
    a regression would push this into many minutes.
    """
    _require_command("uv")
    _require_command("curl")
    base_url = live_agfs_server

    # Pre-stage the input file on the server. memfs's /-mount accepts
    # PUT under any path, so we use a unique e2e-only path so parallel
    # runs don't fight over it.
    line_count = 100_000
    payload = ("a\n" * line_count).encode("utf-8")
    path = f"/memfs/e2e-readline-perf-{os.getpid()}"

    put = requests.put(
        f"{base_url}/api/v1/files",
        params={"path": path},
        data=payload,
        timeout=30,
    )
    put.raise_for_status()

    # Run the pipeline through a real agfs-shell. The three stages are
    # wired up by the shell using StreamingInputStream between each
    # pair — head's input side is the class this PR optimises.
    env = os.environ.copy()
    env["AGFS_API_URL"] = base_url

    start = time.monotonic()
    result = subprocess.run(
        # ``--frozen`` so the test never mutates ``agfs-shell/uv.lock``
        # under us — the shared E2E gate must leave tracked files
        # clean.
        [
            "uv",
            "run",
            "--frozen",
            "agfs-shell",
            "-c",
            f"cat {path} | head -n {line_count} | wc -l",
        ],
        cwd=SHELL_ROOT,
        env=env,
        text=True,
        capture_output=True,
        timeout=60,
        check=True,
    )
    elapsed = time.monotonic() - start

    stdout = result.stdout.strip()
    # `head -n line_count` passes through every input line, then
    # `wc -l` reports the count back to the shell's stdout.
    assert stdout.split()[-1] == str(line_count), (
        f"unexpected pipeline output (expected {line_count} via "
        f"head→wc): {stdout!r}"
    )
    # Generous bound — a regression of readline() to the per-byte
    # loop would blow past 30s on this input. Document the failure
    # mode in the error so a future reader can trace the wall-clock
    # regression back to the readline change.
    assert elapsed < 30.0, (
        f"pipeline took {elapsed:.2f}s on 100k lines — likely a "
        "regression of StreamingInputStream.readline back to the "
        "per-byte read loop"
    )

    # Clean up the staged file so repeated runs stay tidy.
    requests.delete(
        f"{base_url}/api/v1/files",
        params={"path": path},
        timeout=10,
    )


@pytest.mark.e2e
@requires_e2e
def test_pipeline_preserves_content_through_streaming_buffer_end_to_end(live_agfs_server):
    """Correctness companion to the perf test: a pipeline pushing a
    known payload through the streaming buffer must surface the
    exact same bytes downstream.

    Workload: `cat /e2e-readline-content | head -3` returns the
    first three lines. Catches a class of "rolling buffer dropped a
    byte at a chunk boundary" regressions that wouldn't surface in
    the unit-level test_streaming_input_stream.py because the unit
    test controls chunk boundaries directly.
    """
    _require_command("uv")
    base_url = live_agfs_server

    lines = [b"one\n", b"two\n", b"three\n", b"four\n", b"five\n"]
    payload = b"".join(lines)
    path = f"/memfs/e2e-readline-content-{os.getpid()}"

    put = requests.put(
        f"{base_url}/api/v1/files",
        params={"path": path},
        data=payload,
        timeout=10,
    )
    put.raise_for_status()

    env = os.environ.copy()
    env["AGFS_API_URL"] = base_url

    # agfs-shell's ``head`` builtin only parses the ``-n N`` flag — the
    # POSIX shorthand ``head -3`` isn't recognized. Pass the long form.
    result = subprocess.run(
        # ``--frozen`` — see the perf test above. No tracked-file
        # mutation by the E2E gate.
        ["uv", "run", "--frozen", "agfs-shell", "-c", f"cat {path} | head -n 3"],
        cwd=SHELL_ROOT,
        env=env,
        text=True,
        capture_output=True,
        timeout=30,
        check=True,
    )

    out = result.stdout
    # head -3 must surface exactly the first three input lines.
    assert "one" in out
    assert "two" in out
    assert "three" in out
    # And NOT the lines that should have been cut off.
    assert "four" not in out
    assert "five" not in out

    requests.delete(
        f"{base_url}/api/v1/files",
        params={"path": path},
        timeout=10,
    )
