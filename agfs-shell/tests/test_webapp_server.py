"""Tests for integrated webapp startup preflight checks."""

import pytest

from agfs_shell.webapp_server import validate_webapp_dist


def test_validate_webapp_dist_requires_index_html(tmp_path):
    dist_dir = tmp_path / "dist"
    dist_dir.mkdir()

    with pytest.raises(RuntimeError) as exc_info:
        validate_webapp_dist(dist_dir)

    message = str(exc_info.value)
    assert "Web app build output not found" in message
    assert str(dist_dir / "index.html") in message
    assert "cd agfs-shell/webapp" in message
    assert "npm ci" in message
    assert "npm run build" in message
    assert "./setup.sh" in message


def test_validate_webapp_dist_accepts_built_webapp(tmp_path):
    dist_dir = tmp_path / "dist"
    dist_dir.mkdir()
    (dist_dir / "index.html").write_text("<!doctype html>", encoding="utf-8")

    assert validate_webapp_dist(dist_dir) == dist_dir
