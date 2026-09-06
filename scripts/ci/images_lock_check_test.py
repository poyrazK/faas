#!/usr/bin/env python3
"""Tests for images_lock_check.py + audit_dockerfile_froms.py.

Run with:
  python3 scripts/ci/images_lock_check_test.py

The tests synthesize a temp repo with a lock + Dockerfiles, run the
checks, and assert the expected pass/fail behavior. Pure stdlib.

These are NOT pytest tests — the project doesn't use pytest; they're
plain `python3 -m unittest` tests invoked by the Makefile via
`make images-lock-check-tests`.
"""
from __future__ import annotations

import email.message
import importlib.util
import json
import shutil
import subprocess
import sys
import tempfile
import unittest
import urllib.error
from pathlib import Path
from unittest import mock

SCRIPTS = Path(__file__).resolve().parent


def _load_lock_update_module():
    path = SCRIPTS / "images_lock_update.py"
    spec = importlib.util.spec_from_file_location("images_lock_update", path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load {path}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def _run(script: str, *args: str, cwd: Path) -> subprocess.CompletedProcess:
    return subprocess.run(
        [sys.executable, str(SCRIPTS / script), *args],
        cwd=cwd, capture_output=True, text=True,
    )


def _write_lock(repo: Path, entries: list[dict]) -> Path:
    (repo / "images").mkdir(parents=True, exist_ok=True)
    lock = {
        "$schema": "https://json-schema.org/draft/2020-12/schema",
        "version": 1,
        "generated_at": "2026-07-26T00:00:00Z",
        "pinned": entries,
    }
    lock_path = repo / "images" / "Dockerfile.lock"
    lock_path.write_text(json.dumps(lock, indent=2))
    return lock_path


def _write_dockerfile(repo: Path, name: str, body: str) -> Path:
    (repo / "images").mkdir(parents=True, exist_ok=True)
    path = repo / "images" / name
    path.write_text(body)
    return path


class TestImagesLockCheck(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = Path(tempfile.mkdtemp(prefix="images-lock-test-"))

    def tearDown(self) -> None:
        shutil.rmtree(self.tmp, ignore_errors=True)

    def test_lock_missing_entry_reports_error(self) -> None:
        # Dockerfile has a non-pinned FROM, the lock is empty.
        _write_dockerfile(self.tmp, "a.Dockerfile", "FROM debian:12-slim\n")
        _write_lock(self.tmp, [])
        r = _run("audit_dockerfile_froms.py", "--repo-root", str(self.tmp), cwd=self.tmp)
        self.assertNotEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertIn("mutable tag", r.stderr)

    def test_lock_and_dockerfile_match_pass(self) -> None:
        # Digest-pinned, lock matches Dockerfile.
        real_digest = "9d3e2b29c1a0d4b8e7c6f1a3b5d2e8c4f7a9b1c3d5e7f9a1b3c5d7e9f1a3b5c7"
        body = f"FROM debian:12-slim@sha256:{real_digest}\nRUN echo ok\n"
        _write_dockerfile(self.tmp, "a.Dockerfile", body)
        _write_lock(self.tmp, [{
            "dockerfile": "images/a.Dockerfile",
            "instruction": "FROM",
            "alias": "debian:12-slim",
            "resolved_repo": "docker.io/library/debian",
            "resolved_tag": "12-slim",
            "platform": "linux/amd64",
            "digest": f"sha256:{real_digest}",
            "pinned_in_dockerfile": body.splitlines()[0],
        }])
        r = _run("audit_dockerfile_froms.py", "--repo-root", str(self.tmp), cwd=self.tmp)
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        r = _run("images_lock_check.py", "--repo-root", str(self.tmp), cwd=self.tmp)
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)

    def test_placeholder_digest_fails(self) -> None:
        body = "FROM debian:12-slim@sha256:REPLACE_ME_AT_MERGE_TIME\n"
        _write_dockerfile(self.tmp, "a.Dockerfile", body)
        _write_lock(self.tmp, [{
            "dockerfile": "images/a.Dockerfile",
            "instruction": "FROM",
            "alias": "debian:12-slim",
            "resolved_repo": "docker.io/library/debian",
            "resolved_tag": "12-slim",
            "platform": "linux/amd64",
            "digest": "sha256:REPLACE_ME_AT_MERGE_TIME",
            "pinned_in_dockerfile": "FROM debian:12-slim@sha256:REPLACE_ME_AT_MERGE_TIME",
        }])
        r = _run("images_lock_check.py", "--repo-root", str(self.tmp), cwd=self.tmp)
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("REPLACE_ME", r.stderr)

    def test_scratch_is_exempt(self) -> None:
        # scratch is the canonical empty image; audit should pass
        # without requiring a lock entry.
        real_digest = "9d3e2b29c1a0d4b8e7c6f1a3b5d2e8c4f7a9b1c3d5e7f9a1b3c5d7e9f1a3b5c7"
        _write_dockerfile(self.tmp, "a.Dockerfile",
                          f"FROM debian:12-slim@sha256:{real_digest} AS build\n"
                          "FROM scratch\n")
        _write_lock(self.tmp, [{
            "dockerfile": "images/a.Dockerfile",
            "instruction": "FROM",
            "alias": "debian:12-slim",
            "resolved_repo": "docker.io/library/debian",
            "resolved_tag": "12-slim",
            "platform": "linux/amd64",
            "digest": f"sha256:{real_digest}",
            "pinned_in_dockerfile": f"FROM debian:12-slim@sha256:{real_digest} AS build",
        }])
        r = _run("audit_dockerfile_froms.py", "--repo-root", str(self.tmp), cwd=self.tmp)
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)

    def test_drift_dockerfile_does_not_match_lock_fails(self) -> None:
        # Dockerfile line is digest-pinned, but to a DIFFERENT digest
        # than the lock.
        real_a = "9d3e2b29c1a0d4b8e7c6f1a3b5d2e8c4f7a9b1c3d5e7f9a1b3c5d7e9f1a3b5c7"
        real_b = "1111111111111111111111111111111111111111111111111111111111111111"
        body = f"FROM debian:12-slim@sha256:{real_a}\n"
        _write_dockerfile(self.tmp, "a.Dockerfile", body)
        _write_lock(self.tmp, [{
            "dockerfile": "images/a.Dockerfile",
            "instruction": "FROM",
            "alias": "debian:12-slim",
            "resolved_repo": "docker.io/library/debian",
            "resolved_tag": "12-slim",
            "platform": "linux/amd64",
            "digest": f"sha256:{real_b}",
            "pinned_in_dockerfile": f"FROM debian:12-slim@sha256:{real_b}",
        }])
        r = _run("images_lock_check.py", "--repo-root", str(self.tmp), cwd=self.tmp)
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("does not match lock", r.stderr)


class TestImagesLockUpdatePlatformSelection(unittest.TestCase):
    def test_manifest_list_resolves_requested_child(self) -> None:
        update = _load_lock_update_module()
        manifest = {
            "manifests": [
                {"digest": "sha256:arm", "platform": {"os": "linux", "architecture": "arm64"}},
                {"digest": "sha256:x86", "platform": {"os": "linux", "architecture": "amd64"}},
            ]
        }
        self.assertEqual(
            update._select_platform_digest(manifest, "linux/amd64"),
            "sha256:x86",
        )

    def test_manifest_list_without_matching_child_returns_none(self) -> None:
        update = _load_lock_update_module()
        manifest = {
            "manifests": [
                {"digest": "sha256:arm", "platform": {"os": "linux", "architecture": "arm64"}},
            ]
        }
        self.assertIsNone(update._select_platform_digest(manifest, "linux/amd64"))

    def test_non_docker_registry_bearer_auth_resolves_child(self) -> None:
        update = _load_lock_update_module()
        response_headers = email.message.Message()
        response_headers.add_header(
            "WWW-Authenticate",
            'Bearer realm="https://cgr.example/token",service="cgr.example",'
            'scope="repository:chainguard/bash:pull"',
        )

        class Response:
            def __init__(self, body: dict, headers: dict[str, str] | None = None):
                self.body = json.dumps(body).encode()
                self.headers = headers or {}

            def __enter__(self):
                return self

            def __exit__(self, *_args):
                return False

            def read(self) -> bytes:
                return self.body

        requests: list = []

        def open_registry(request, timeout):
            self.assertIn(timeout, (10, 15))
            requests.append(request)
            if len(requests) == 1:
                raise urllib.error.HTTPError(
                    request.full_url, 401, "Unauthorized", response_headers, None
                )
            if len(requests) == 2:
                self.assertEqual(request.full_url.split("?", 1)[0], "https://cgr.example/token")
                return Response({"token": "registry-token"})
            self.assertEqual(request.headers["Authorization"], "Bearer registry-token")
            return Response({
                "manifests": [
                    {"digest": "sha256:arm", "platform": {"os": "linux", "architecture": "arm64"}},
                    {"digest": "sha256:x86", "platform": {"os": "linux", "architecture": "amd64"}},
                ]
            })

        with mock.patch.object(
            update, "_open_registry_request", side_effect=open_registry
        ):
            digest = update.resolve_via_registry_api(
                "cgr.example/chainguard/bash", "latest", "linux/amd64"
            )

        self.assertEqual(digest, "sha256:x86")
        self.assertEqual(len(requests), 3)

    def test_bearer_auth_rejects_untrusted_or_insecure_realm(self) -> None:
        update = _load_lock_update_module()
        auth = {"Authorization": "Basic secret"}
        for realm in (
            "https://evil.example/token",
            "http://cgr.example/token",
            "https://cgr.example:8443/token",
        ):
            challenge = f'Bearer realm="{realm}",service="cgr.example"'
            with mock.patch.object(update, "_open_registry_request") as open_registry:
                self.assertIsNone(
                    update._registry_bearer_token(challenge, "cgr.example", auth)
                )
                open_registry.assert_not_called()

    def test_bearer_redirect_strips_cross_origin_authorization(self) -> None:
        update = _load_lock_update_module()
        request = urllib.request.Request(
            "https://cgr.example/token", headers={"Authorization": "Basic secret"}
        )
        headers = email.message.Message()
        redirected = update._AuthSafeRedirectHandler().redirect_request(
            request, None, 302, "Found", headers, "https://evil.example/token"
        )
        self.assertIsNotNone(redirected)
        self.assertIsNone(redirected.get_header("Authorization"))


if __name__ == "__main__":
    unittest.main(verbosity=2)
