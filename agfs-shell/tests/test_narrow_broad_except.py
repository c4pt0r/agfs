"""
Regression tests for narrowed broad-Exception catches in agfs-shell.

Before task #15 these call sites swallowed every exception type as if
it were the documented "external boundary" failure mode (path missing,
network blip, etc.), silently steering the caller down the wrong path
when a programmer error (AttributeError, TypeError, ...) actually
fired. After the narrowing, only ``AGFSClientError`` — and a small
explicit allow-list where the original intent was filesystem-level —
is silenced. Anything else propagates to the command's outer boundary
where the user sees a real error message and the command exits
non-zero.
"""

from __future__ import annotations

import io
from types import SimpleNamespace
from unittest.mock import MagicMock

import pytest

from pyagfs.exceptions import AGFSClientError

from agfs_shell.commands.upload import cmd_upload
from agfs_shell.commands.ls import cmd_ls


def _make_process(args, filesystem, stdout=None, stderr=None, cwd="/"):
    """Build a minimal Process-like object good enough for upload/ls."""
    stdout = stdout or io.BytesIO()
    stderr = stderr or io.BytesIO()

    class _OutAdapter:
        def __init__(self, buf):
            self._buf = buf

        def write(self, data):
            if isinstance(data, str):
                data = data.encode("utf-8")
            self._buf.write(data)
            return len(data)

        def flush(self):
            pass

        @property
        def value(self):
            return self._buf.getvalue()

    return SimpleNamespace(
        args=list(args),
        cwd=cwd,
        stdout=_OutAdapter(stdout),
        stderr=_OutAdapter(stderr),
        context=SimpleNamespace(filesystem=filesystem, cwd=cwd),
    )


class TestUploadNarrowedExcept:
    """``cmd_upload`` must only silence ``AGFSClientError`` from
    ``get_file_info`` — other exception types are real bugs and must
    surface so the user sees a real error instead of the upload
    silently steering to the wrong path."""

    def test_destination_missing_via_agfs_error_falls_through(self, tmp_path):
        """The documented "destination doesn't exist" path is still
        silently allowed (AGFSClientError ⇒ use the user-supplied
        agfs_path verbatim)."""
        local = tmp_path / "src.txt"
        local.write_bytes(b"hello")

        fs = MagicMock()
        fs.get_file_info.side_effect = AGFSClientError("not found")
        # write_file accepts the upload.
        fs.write_file.return_value = None

        proc = _make_process([str(local), "/dst.txt"], fs)
        rc = cmd_upload(proc)

        assert rc == 0
        # write_file was called with the user-supplied path, NOT with
        # a directory-appended variant.
        fs.write_file.assert_called_once()
        assert fs.write_file.call_args.args[0] == "/dst.txt"

    def test_destination_lookup_unexpected_error_surfaces(self, tmp_path):
        """A programmer-error exception type from ``get_file_info``
        (e.g. ``AttributeError``) used to be silently swallowed.
        Now it bubbles to the command boundary, which writes a real
        error to stderr and returns non-zero. The upload must NOT
        proceed against a broken filesystem object."""
        local = tmp_path / "src.txt"
        local.write_bytes(b"hello")

        fs = MagicMock()
        fs.get_file_info.side_effect = AttributeError("filesystem is broken")
        fs.write_file.return_value = None

        proc = _make_process([str(local), "/dst.txt"], fs)
        rc = cmd_upload(proc)

        assert rc != 0
        assert b"filesystem is broken" in proc.stderr.value
        fs.write_file.assert_not_called()


class TestLsReadlinkNarrowedExcept:
    """``cmd_ls`` falls back to a plain-file display when
    ``readlink`` raises ``AGFSClientError`` (legitimate filesystem
    error), but propagates anything else — covered by the outer
    command boundary."""

    def _make_fs_with_symlink(self, readlink_side_effect):
        fs = MagicMock()
        # Top-level path is a directory.
        fs.get_file_info.return_value = {
            "name": "/",
            "isDir": True,
            "type": "directory",
            "size": 0,
            "mode": 0o755,
            "modTime": "",
        }
        # Single symlink child.
        fs.list_directory.return_value = [
            {
                "name": "link",
                "isDir": False,
                "size": 0,
                "mode": 0o777,
                "modTime": "",
                "meta": {"Type": "symlink"},
            }
        ]
        fs.readlink.side_effect = readlink_side_effect
        return fs

    def test_readlink_agfs_error_falls_back_to_plain(self):
        """``AGFSClientError`` from ``readlink`` is treated as
        "not really a symlink we can resolve" — display continues
        without the `→ target` arrow."""
        fs = self._make_fs_with_symlink(AGFSClientError("not a symlink"))
        proc = _make_process(["-l", "/"], fs)
        rc = cmd_ls(proc)

        assert rc == 0
        out = proc.stdout.value.decode("utf-8", errors="replace")
        assert "link" in out
        # When readlink fails with AGFSClientError, the display falls
        # back to a plain-file listing and the "->" target marker is
        # not emitted.
        assert "->" not in out

    def test_readlink_unexpected_error_surfaces(self):
        """A non-AGFS exception type from ``readlink`` (programmer
        error, broken mock, ...) must NOT be silently turned into a
        plain-file display — it propagates to the per-path inner
        ``except Exception`` block, which writes an ls error and sets
        the command's exit code non-zero."""
        fs = self._make_fs_with_symlink(RuntimeError("inner readlink bug"))
        proc = _make_process(["-l", "/"], fs)
        rc = cmd_ls(proc)

        assert rc != 0
        assert b"inner readlink bug" in proc.stderr.value


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
