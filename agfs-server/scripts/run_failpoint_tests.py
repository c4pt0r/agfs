#!/usr/bin/env python3

"""Run all agfs-server failpoint tests.

This script rewrites the repository with ``failpoint-ctl enable .``, discovers
tests declared in ``*_failpoint_test.go`` files, runs only those tests with the
``failpoint`` build tag, and then restores the tree with ``failpoint-ctl
disable .``.
"""

from __future__ import annotations

import pathlib
import re
import subprocess
import sys
from collections import defaultdict


def main() -> int:
    # The script always operates from the agfs-server repo root so discovery,
    # failpoint rewriting, and `go test` all target the same tree.
    repo_root = pathlib.Path(__file__).resolve().parent.parent
    failpoint_ctl = (
        pathlib.Path(
            subprocess.check_output(["go", "env", "GOPATH"], text=True).strip()
        )
        / "bin"
        / "failpoint-ctl"
    )
    if not failpoint_ctl.exists():
        print(f"failpoint-ctl not found at {failpoint_ctl}", file=sys.stderr)
        print(
            "Install it with: go install github.com/pingcap/failpoint/failpoint-ctl@latest",
            file=sys.stderr,
        )
        return 1

    tests_by_pkg = collect_failpoint_tests(repo_root)
    if not tests_by_pkg:
        print("no *_failpoint_test.go files found", file=sys.stderr)
        return 1

    exit_code = 0
    try:
        # Enable failpoint rewriting for the whole repo before running any test;
        # failpoint tests run in an instrumented tree.
        run([str(failpoint_ctl), "enable", "."], cwd=repo_root)
        for pkg, tests in sorted(tests_by_pkg.items()):
            pattern = "^(%s)$" % "|".join(sorted(tests))
            run(
                ["go", "test", "-v", "-tags", "failpoint", pkg, "-run", pattern],
                cwd=repo_root,
            )
    except subprocess.CalledProcessError as exc:
        exit_code = exc.returncode or 1
    finally:
        # Always restore the tree, even if discovery or a test run fails.
        subprocess.run([str(failpoint_ctl), "disable", "."], cwd=repo_root, check=False)

    return exit_code


def collect_failpoint_tests(repo_root: pathlib.Path) -> dict[str, set[str]]:
    # Only tests defined in *_failpoint_test.go should run under the project-wide
    # failpoint entrypoint; ordinary *_test.go files stay in the regular test path.
    func_pattern = re.compile(r"^func\s+(Test\w+)\s*\(.*\*testing\.T\)")
    tests_by_pkg: dict[str, set[str]] = defaultdict(set)

    for path in repo_root.rglob("*_failpoint_test.go"):
        rel_dir = path.parent.relative_to(repo_root)
        pkg = "./" + rel_dir.as_posix() if rel_dir.as_posix() != "." else "./"
        for line in path.read_text(encoding="utf-8").splitlines():
            match = func_pattern.match(line.strip())
            if match:
                tests_by_pkg[pkg].add(match.group(1))

    return tests_by_pkg


def run(cmd: list[str], cwd: pathlib.Path) -> None:
    subprocess.run(cmd, cwd=cwd, check=True)


if __name__ == "__main__":
    raise SystemExit(main())
