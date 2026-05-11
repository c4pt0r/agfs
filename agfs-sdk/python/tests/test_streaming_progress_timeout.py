"""
Focused tests for the SDK's streaming progress timeout.

Before this change, ``AGFSClient.cat(stream=True)`` and friends passed
``timeout=None`` to ``requests``, which meant a server that stopped
sending bytes (or never sent any) could wedge the client forever. The
new ``streaming_progress_timeout`` constructor argument enables a
per-chunk inactivity timeout via the ``(connect_timeout, read_timeout)``
tuple that ``requests`` supports natively.

These tests stand up a tiny local HTTP server that deliberately stalls
mid-stream and assert the client's ``iter_content`` loop raises
``ReadTimeout`` rather than hanging.
"""

from __future__ import annotations

import threading
import time
from http.server import BaseHTTPRequestHandler, HTTPServer

import pytest
import requests

from pyagfs import AGFSClient


class _StallingHandler(BaseHTTPRequestHandler):
    """Always responds 200 + chunked-encoding headers, then sleeps so
    the body never arrives.

    The handler advertises Transfer-Encoding: chunked and writes one
    zero-byte placeholder so requests/urllib3 commits to reading the
    body, then sleeps for ``STALL_SECONDS`` before sending any real
    data. That is the exact "stalled stream" failure mode the progress
    timeout is meant to bound.
    """

    STALL_SECONDS = 5

    def do_GET(self):
        self.send_response(200)
        self.send_header("Content-Type", "application/octet-stream")
        self.send_header("Transfer-Encoding", "chunked")
        self.end_headers()
        # Write nothing — let the client block on the first chunk read.
        # Sleep longer than any reasonable test timeout so the test's
        # progress-timeout assertion is the only way it can complete.
        time.sleep(self.STALL_SECONDS)

    def log_message(self, format, *args):  # noqa: A002 - matches stdlib
        # Silence default access-log noise during the test.
        return


@pytest.fixture
def stalling_server():
    """Spin up the stalling server on a free port, hand back its base URL,
    and tear it down on test exit."""

    server = HTTPServer(("127.0.0.1", 0), _StallingHandler)
    port = server.server_address[1]
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield f"http://127.0.0.1:{port}"
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=1)


def test_streaming_read_progress_timeout_fires(stalling_server):
    """A stalled server triggers a timeout when iterating the response,
    rather than hanging until the test suite timeout kills the run.

    The exact exception type depends on transport state: for a
    chunked-encoding stream that's mid-body, urllib3's
    ``ReadTimeoutError`` is wrapped by ``requests`` as
    :class:`requests.exceptions.ConnectionError` carrying a "Read timed
    out" message. For Content-Length responses it surfaces as
    :class:`requests.exceptions.ReadTimeout`. We pin the user-visible
    contract — a ``RequestException`` carrying the words ``timed out``
    — rather than the exact subclass, so a future transport change
    can't silently regress the bound.
    """
    client = AGFSClient(
        api_base_url=stalling_server,
        timeout=2,
        streaming_progress_timeout=0.3,
    )

    response = client.cat("/anything", stream=True)
    start = time.monotonic()
    with pytest.raises(requests.exceptions.RequestException) as exc_info:
        # Force a body read. The first chunk never arrives, so requests
        # waits up to ``read_timeout`` for any bytes and then raises.
        for _ in response.iter_content(chunk_size=1024):
            pass
    elapsed = time.monotonic() - start
    assert "timed out" in str(exc_info.value).lower(), (
        f"expected a timeout error message, got: {exc_info.value!r}"
    )
    # Sanity: we waited about the configured progress timeout, not the
    # server's full stall window.
    assert elapsed < _StallingHandler.STALL_SECONDS, (
        f"streaming_progress_timeout did not fire — waited {elapsed:.2f}s"
    )


def test_streaming_timeout_opt_out_preserves_legacy_behaviour(stalling_server):
    """Setting ``streaming_progress_timeout=None`` restores the
    pre-2026-05 ``timeout=None`` behaviour for callers that need it.

    We bound the test wait at sub-stall-window seconds and assert the
    client is still inside the body-read loop (i.e. ``ReadTimeout`` did
    NOT fire) — proving the opt-out is honoured.
    """
    client = AGFSClient(
        api_base_url=stalling_server,
        timeout=2,
        streaming_progress_timeout=None,
    )

    response = client.cat("/anything", stream=True)

    def consume():
        try:
            for _ in response.iter_content(chunk_size=1024):
                pass
        except requests.exceptions.RequestException:
            pass

    worker = threading.Thread(target=consume, daemon=True)
    worker.start()
    # Wait less than the server's stall so we don't actually receive
    # data. If the opt-out is broken (a default progress timeout still
    # applies), this thread joins early on ReadTimeout.
    worker.join(timeout=0.8)
    assert worker.is_alive(), (
        "consume thread exited before stall completed — "
        "streaming_progress_timeout=None did not opt out"
    )


def test_streaming_timeout_tuple_uses_connect_and_progress_legs():
    """The internal helper builds a ``(connect, read)`` tuple from
    ``timeout`` and ``streaming_progress_timeout``. Pin the shape so a
    future refactor can't silently merge them back into a single value.
    """
    c = AGFSClient(
        api_base_url="http://example.invalid",
        timeout=11,
        streaming_progress_timeout=22,
    )
    assert c._streaming_timeout() == (11, 22)

    c_opt_out = AGFSClient(
        api_base_url="http://example.invalid",
        streaming_progress_timeout=None,
    )
    assert c_opt_out._streaming_timeout() is None
