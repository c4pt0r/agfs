"""
Tests for ``AGFSClient`` lifecycle: ``close()``, ``__enter__``,
``__exit__``, and the ``is_closed`` property.

Before task #21 there was no documented way to release the
``requests.Session`` that ``AGFSClient`` keeps for the lifetime of
the object. Long-lived processes (FUSE mounts, agents) leaked
HTTP keep-alive sockets at shutdown; ``with AGFSClient(...) as c:``
— the idiomatic Python pattern users expect — raised
``AttributeError`` because the dunder methods weren't defined.

These tests pin the new contract:

* ``close()`` releases the session and is idempotent.
* ``__enter__`` / ``__exit__`` make the client usable as a context
  manager that closes the session on exit, even when the block
  raises.
* ``is_closed`` reflects the lifecycle state.
"""

from __future__ import annotations

from unittest.mock import MagicMock

import pytest

from pyagfs import AGFSClient
from pyagfs.exceptions import AGFSClientError


class TestClose:
    """Direct ``close()`` calls release the session and are idempotent."""

    def test_close_closes_underlying_session(self):
        c = AGFSClient(api_base_url="http://example.invalid")
        real_session = c.session
        sentinel = MagicMock()
        # Wrap the real Session so we can verify close() is forwarded
        # without rebuilding the whole requests.Session API surface.
        c.session = sentinel
        try:
            c.close()
        finally:
            # Restore so any subsequent cleanup hooks don't error.
            c.session = real_session
        sentinel.close.assert_called_once()

    def test_close_is_idempotent(self):
        c = AGFSClient(api_base_url="http://example.invalid")
        sentinel = MagicMock()
        c.session = sentinel
        c.close()
        c.close()
        c.close()
        # The wrapped Session.close is only forwarded the first time —
        # subsequent close() calls short-circuit on self._closed.
        sentinel.close.assert_called_once()

    def test_close_marks_client_closed(self):
        c = AGFSClient(api_base_url="http://example.invalid")
        assert c.is_closed is False
        c.close()
        assert c.is_closed is True

    def test_close_marks_closed_even_if_session_close_raises(self):
        """If the underlying Session.close raises (e.g. a bug in a
        custom adapter), we still mark the client closed so a retry
        loop doesn't trigger the same failure path repeatedly."""
        c = AGFSClient(api_base_url="http://example.invalid")
        sentinel = MagicMock()
        sentinel.close.side_effect = RuntimeError("adapter died")
        c.session = sentinel
        with pytest.raises(RuntimeError, match="adapter died"):
            c.close()
        assert c.is_closed is True
        # Second close is a no-op even after the first one raised.
        c.close()


class TestContextManager:
    """``with AGFSClient(...) as c:`` closes the session on exit."""

    def test_enter_returns_self(self):
        c = AGFSClient(api_base_url="http://example.invalid")
        try:
            entered = c.__enter__()
            assert entered is c
        finally:
            c.close()

    def test_with_block_closes_on_normal_exit(self):
        sentinel = MagicMock()
        c = AGFSClient(api_base_url="http://example.invalid")
        c.session = sentinel
        with c as entered:
            assert entered is c
            assert entered.is_closed is False
        sentinel.close.assert_called_once()
        assert c.is_closed is True

    def test_with_block_closes_on_exception(self):
        """The session must close even when the ``with`` body raises.
        ``__exit__`` returns ``None`` (falsy) so the original
        exception propagates — we do not silence caller errors."""
        sentinel = MagicMock()
        c = AGFSClient(api_base_url="http://example.invalid")
        c.session = sentinel

        with pytest.raises(ValueError, match="user code blew up"):
            with c:
                raise ValueError("user code blew up")

        sentinel.close.assert_called_once()
        assert c.is_closed is True

    def test_nested_with_uses_idempotent_close(self):
        """Two nested ``with`` blocks on the same client (an unusual
        but legal pattern) must not double-close the session."""
        sentinel = MagicMock()
        c = AGFSClient(api_base_url="http://example.invalid")
        c.session = sentinel

        with c:
            with c:
                pass
            # Inner __exit__ already closed; outer __exit__ closes again
            # but the call is idempotent.
            assert c.is_closed is True

        sentinel.close.assert_called_once()


class TestPostCloseGuard:
    """After ``close()``, every HTTP method must raise a clear error
    instead of issuing a request against a stale Session — which
    ``requests`` itself does not actively prevent.

    Implementation strategy: ``close()`` swaps ``self.session`` for a
    ``_ClosedSession`` sentinel whose attribute accesses raise
    ``AGFSClientError``. Any ``self.session.get(...)`` /
    ``self.session.post(...)`` call shape in the SDK fails fast there.
    """

    def _fake_health_response(self):
        resp = MagicMock()
        resp.json.return_value = {"status": "ok"}
        resp.raise_for_status.return_value = None
        return resp

    def test_health_after_close_raises_agfs_client_error(self):
        """Repro of @dev-1's blocking finding: previously, ``c.health()``
        succeeded after ``close()`` because ``c.session`` was still the
        live ``requests.Session``. The sentinel now intercepts."""
        c = AGFSClient(api_base_url="http://example.invalid")
        session = MagicMock()
        session.get.return_value = self._fake_health_response()
        c.session = session

        c.close()
        with pytest.raises(AGFSClientError, match="has been closed"):
            c.health()
        # The live session.get must NOT have been touched after close.
        session.get.assert_not_called()

    def test_ls_after_close_raises_agfs_client_error(self):
        """Cover a second method shape (``ls`` uses ``self.session.get``
        as well) to demonstrate the sentinel intercepts at the
        ``self.session`` attribute access, not at a single method."""
        c = AGFSClient(api_base_url="http://example.invalid")
        session = MagicMock()
        c.session = session

        c.close()
        with pytest.raises(AGFSClientError, match="has been closed"):
            c.ls("/")
        session.get.assert_not_called()

    def test_sentinel_replaces_session_attribute_after_close(self):
        """Pin the implementation contract: ``self.session`` is the
        sentinel object after ``close()``. Future refactors that
        rebuild the lifecycle on different machinery must keep this
        invariant or replace it with an equivalent guard."""
        from pyagfs.client import _ClosedSession

        c = AGFSClient(api_base_url="http://example.invalid")
        sentinel = MagicMock()
        c.session = sentinel
        c.close()
        assert isinstance(c.session, _ClosedSession)
        # Attribute access on the sentinel raises directly.
        with pytest.raises(AGFSClientError, match="has been closed"):
            c.session.get("http://example.invalid/anything")
        # And the close() call did forward to the original Session once.
        sentinel.close.assert_called_once()

    def test_sentinel_swap_survives_session_close_raising(self):
        """Even if the underlying ``Session.close`` raises, the sentinel
        swap and the ``_closed`` flip still happen — closing the door
        on use-after-error retry loops."""
        from pyagfs.client import _ClosedSession

        c = AGFSClient(api_base_url="http://example.invalid")
        sentinel = MagicMock()
        sentinel.close.side_effect = RuntimeError("adapter died")
        c.session = sentinel

        with pytest.raises(RuntimeError, match="adapter died"):
            c.close()

        assert c.is_closed is True
        assert isinstance(c.session, _ClosedSession)
        with pytest.raises(AGFSClientError, match="has been closed"):
            c.health()
