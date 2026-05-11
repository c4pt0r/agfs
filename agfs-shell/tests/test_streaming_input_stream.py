"""
Tests for ``StreamingInputStream`` — the queue-backed input stream that
pipelines feed into commands.

The class supports three read shapes (``read(size)``, ``read(-1)``,
``readline()``, ``readlines()``) and shares buffer state across them.
Task #20 optimised ``readline()`` from an O(N) ``read(1)`` loop to an
O(line length) ``bytearray.find`` over a rolling buffer. These tests
pin both:

1. Correctness — every read shape returns the same bytes that the
   producer pushed, regardless of how the chunk boundaries land
   relative to newlines or to the requested ``size``.
2. Performance — a large line that previously caused quadratic
   behaviour now completes in well under a generous time budget.
"""

from __future__ import annotations

import queue
import time

import pytest

from agfs_shell.pipeline import StreamingInputStream


def _feed(chunks):
    """Build a queue pre-filled with ``chunks`` followed by the EOF
    sentinel (``None``) — exactly what ``StreamingPipeline`` pushes."""
    q = queue.Queue()
    for c in chunks:
        q.put(c)
    q.put(None)  # EOF sentinel
    return q


class TestStreamingInputStreamCorrectness:
    """Read shapes match what the producer pushed, across chunk
    boundary cases that the rolling-buffer rewrite has to get right."""

    def test_readline_single_chunk_single_line(self):
        s = StreamingInputStream(_feed([b"hello\n"]))
        assert s.readline() == b"hello\n"
        assert s.readline() == b""  # EOF

    def test_readline_chunks_split_inside_line(self):
        """The producer split a line across two chunks. Readline must
        stitch them back together via the rolling buffer."""
        s = StreamingInputStream(_feed([b"hel", b"lo\n"]))
        assert s.readline() == b"hello\n"
        assert s.readline() == b""

    def test_readline_chunks_contain_multiple_lines(self):
        """One chunk carries multiple newlines. Each readline must
        return exactly one line including its newline."""
        s = StreamingInputStream(_feed([b"a\nb\nc\n"]))
        assert s.readline() == b"a\n"
        assert s.readline() == b"b\n"
        assert s.readline() == b"c\n"
        assert s.readline() == b""

    def test_readline_trailing_partial_line_without_newline(self):
        """If the stream ends with bytes that don't include a
        terminator, the final readline returns them and the next
        returns b''."""
        s = StreamingInputStream(_feed([b"first\n", b"trail"]))
        assert s.readline() == b"first\n"
        assert s.readline() == b"trail"
        assert s.readline() == b""

    def test_read_size_across_chunk_boundary(self):
        s = StreamingInputStream(_feed([b"abc", b"def", b"ghi"]))
        assert s.read(2) == b"ab"
        assert s.read(2) == b"cd"
        assert s.read(2) == b"ef"
        assert s.read(2) == b"gh"
        assert s.read(2) == b"i"
        assert s.read(2) == b""

    def test_read_all_drains_pipe(self):
        s = StreamingInputStream(_feed([b"alpha", b"-", b"beta"]))
        assert s.read(-1) == b"alpha-beta"
        # Subsequent reads return b''; class is one-shot per EOF.
        assert s.read(-1) == b""

    def test_read_zero_returns_empty_without_blocking(self):
        s = StreamingInputStream(_feed([b"anything\n"]))
        assert s.read(0) == b""
        # And the queued data is still available afterwards.
        assert s.readline() == b"anything\n"

    def test_read_and_readline_share_buffer(self):
        """A partial ``read(n)`` followed by ``readline()`` must
        consume the same buffered bytes — the rolling buffer is a
        single source of truth, not two independent views."""
        s = StreamingInputStream(_feed([b"hello\nworld\n"]))
        assert s.read(2) == b"he"           # consumes "he"
        assert s.readline() == b"llo\n"     # rest of line 1
        assert s.readline() == b"world\n"   # line 2
        assert s.readline() == b""

    def test_readlines_returns_every_line(self):
        s = StreamingInputStream(_feed([b"x\n", b"y\n", b"z\n"]))
        assert s.readlines() == [b"x\n", b"y\n", b"z\n"]


class TestStreamingInputStreamPerformance:
    """The previous ``readline()`` implementation called ``read(1)``
    per byte, which was both Python-loop-quadratic and triggered an
    ``io.BytesIO`` reallocation per chunk. The rolling buffer makes
    a long line linear in line length.

    The timing assertion below is generous (1 second for 1 MB of
    contiguous data) so it doesn't flake on slow CI; the old
    implementation would have taken many seconds because every byte
    paid a full ``read(1)`` round trip including queue access and
    BytesIO reconstruction.
    """

    def test_long_line_readline_completes_quickly(self):
        # 1 MB of data with the newline at the very end forces the
        # old per-byte loop to make a million calls.
        big_line = b"x" * (1024 * 1024 - 1) + b"\n"
        s = StreamingInputStream(_feed([big_line]))

        start = time.monotonic()
        line = s.readline()
        elapsed = time.monotonic() - start

        assert line == big_line
        # Generous bound: actual measured time on a dev box is ~5ms.
        # A regression to per-byte reads would push this into the
        # multi-second range.
        assert elapsed < 1.0, (
            f"readline on a 1 MB single-line stream took {elapsed:.2f}s — "
            "likely a regression of the O(N) per-byte loop"
        )

    def test_many_short_lines_readline_completes_quickly(self):
        # 100k tiny lines stress the per-line overhead of newline
        # search. With the rolling buffer this is one big ``find``
        # per line; with the old loop it was N reads + N joins per
        # line, all over again.
        line_count = 100_000
        chunk = b"".join(b"a\n" for _ in range(line_count))
        s = StreamingInputStream(_feed([chunk]))

        start = time.monotonic()
        lines = s.readlines()
        elapsed = time.monotonic() - start

        assert len(lines) == line_count
        assert lines[0] == b"a\n"
        assert lines[-1] == b"a\n"
        assert elapsed < 2.0, (
            f"readlines on 100k tiny lines took {elapsed:.2f}s — "
            "likely a regression of the O(N) per-byte loop"
        )


class TestStreamingInputStreamBufferCompaction:
    """The rolling buffer compacts its consumed prefix once the
    consumed offset grows past a threshold so memory stays bounded
    on long streams — a pure implementation detail, pinned here so a
    future refactor that drops the compaction (and reintroduces
    unbounded buffer growth) fails this test."""

    def test_buffer_compacts_after_threshold(self):
        # Two chunks well above the compaction threshold so we can
        # observe the in-place compaction by inspecting the internal
        # buffer length.
        big = b"y" * 10_000
        s = StreamingInputStream(_feed([big, big]))

        # Drain the first chunk via read(size).
        assert s.read(len(big)) == big

        # The next read forces a pull of chunk 2; the prefix from
        # chunk 1 should have been dropped, so len(buf) is bounded
        # by chunk 2's size (plus pending offset).
        assert s.read(1) == b"y"
        assert len(s._buf) <= len(big) + 1, (
            f"buffer did not compact: len={len(s._buf)} expected <= {len(big) + 1}"
        )


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
