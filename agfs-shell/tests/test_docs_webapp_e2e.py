"""E2E coverage for first-run docs and integrated webapp startup."""

import os
import shutil
import socket
import subprocess
import time
import urllib.error
import urllib.request
from pathlib import Path

import pytest


REPO_ROOT = Path(__file__).resolve().parents[2]
SHELL_ROOT = REPO_ROOT / "agfs-shell"
WEBAPP_ROOT = SHELL_ROOT / "webapp"
WEBAPP_DIST = WEBAPP_ROOT / "dist"
FIRST_RUN_DOC = REPO_ROOT / "docs" / "first-run.md"

RUN_E2E = os.getenv("AGFS_RUN_E2E") == "1"
requires_e2e = pytest.mark.skipif(
    not RUN_E2E,
    reason="set AGFS_RUN_E2E=1 to run subprocess-based E2E checks",
)


def _require_command(name: str) -> None:
    if shutil.which(name) is None:
        pytest.skip(f"{name} is required for this E2E test")


def _run(command, cwd: Path, timeout: int = 60, env=None) -> subprocess.CompletedProcess:
    return subprocess.run(
        command,
        cwd=cwd,
        env=env,
        text=True,
        capture_output=True,
        timeout=timeout,
        check=True,
    )


def _free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def _wait_for_http(url: str, proc: subprocess.Popen, timeout: int = 20) -> str:
    deadline = time.monotonic() + timeout
    last_error = None
    while time.monotonic() < deadline:
        if proc.poll() is not None:
            stdout, stderr = proc.communicate()
            raise AssertionError(
                f"integrated webapp exited early with {proc.returncode}\n"
                f"stdout:\n{stdout}\n\nstderr:\n{stderr}"
            )

        try:
            with urllib.request.urlopen(url, timeout=1) as response:
                return response.read().decode("utf-8", errors="replace")
        except (urllib.error.URLError, TimeoutError) as exc:
            last_error = exc
            time.sleep(0.25)

    raise AssertionError(f"timed out waiting for {url}: {last_error}")


def _move_dist_aside():
    backup = None
    if WEBAPP_DIST.exists():
        backup = WEBAPP_ROOT / f"dist.e2e-backup-{os.getpid()}"
        if backup.exists():
            shutil.rmtree(backup)
        WEBAPP_DIST.rename(backup)
    return backup


def _restore_dist(backup):
    if WEBAPP_DIST.exists():
        shutil.rmtree(WEBAPP_DIST)
    if backup is not None:
        backup.rename(WEBAPP_DIST)


@pytest.mark.e2e
@requires_e2e
def test_first_run_docs_webapp_build_and_integrated_startup_e2e():
    _require_command("make")
    _require_command("npm")
    _require_command("uv")

    first_run_doc = FIRST_RUN_DOC.read_text(encoding="utf-8")
    setup_script = (WEBAPP_ROOT / "setup.sh").read_text(encoding="utf-8")
    assert "cd agfs-server && make dev" in first_run_doc
    assert "cd agfs-shell/webapp && npm ci && npm run build" in first_run_doc
    assert "uv run agfs-shell --webapp" in first_run_doc
    assert "webapp/dist/index.html" in first_run_doc
    assert "npm ci" in setup_script

    make_dev = _run(["make", "-n", "-C", "agfs-server", "dev"], cwd=REPO_ROOT)
    assert "go run cmd/server/main.go" in make_dev.stdout
    assert "-c config.example.yaml" in make_dev.stdout

    _run(["uv", "sync", "--extra", "webapp"], cwd=SHELL_ROOT, timeout=120)

    backup = _move_dist_aside()
    try:
        missing_dist = subprocess.run(
            [
                "uv",
                "run",
                "agfs-shell",
                "--webapp",
                "--webapp-host",
                "127.0.0.1",
                "--webapp-port",
                str(_free_port()),
            ],
            cwd=SHELL_ROOT,
            text=True,
            capture_output=True,
            timeout=20,
            check=False,
        )
        missing_output = missing_dist.stdout + missing_dist.stderr
        assert missing_dist.returncode != 0
        assert "Web app build output not found" in missing_output
        assert "webapp/dist/index.html" in missing_output
        assert "npm ci" in missing_output
        assert "npm run build" in missing_output
    finally:
        _restore_dist(backup)

    _run(["npm", "ci"], cwd=WEBAPP_ROOT, timeout=120)
    _run(["npm", "run", "build"], cwd=WEBAPP_ROOT, timeout=120)
    assert (WEBAPP_DIST / "index.html").is_file()

    port = _free_port()
    env = os.environ.copy()
    env["AGFS_API_URL"] = os.getenv("AGFS_API_URL", "http://127.0.0.1:8080")
    proc = subprocess.Popen(
        [
            "uv",
            "run",
            "agfs-shell",
            "--webapp",
            "--webapp-host",
            "127.0.0.1",
            "--webapp-port",
            str(port),
        ],
        cwd=SHELL_ROOT,
        env=env,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )

    try:
        body = _wait_for_http(f"http://127.0.0.1:{port}/", proc)
        assert "<!doctype html>" in body.lower()
    finally:
        if proc.poll() is None:
            proc.terminate()
            try:
                proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait(timeout=10)
