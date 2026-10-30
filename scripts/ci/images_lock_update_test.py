#!/usr/bin/env python3
"""Tests for images_lock_update.py's retries and --allow-partial.

Run with:
  python3 scripts/ci/images_lock_update_test.py

The registry is stubbed: resolve_digest is replaced per test, and the retry
backoff never sleeps. Pure stdlib, plain unittest.
"""
from __future__ import annotations

import importlib.util
import json
import tempfile
import unittest
from pathlib import Path
from unittest import mock

SCRIPT = Path(__file__).resolve().parent / "images_lock_update.py"
spec = importlib.util.spec_from_file_location("images_lock_update", SCRIPT)
update = importlib.util.module_from_spec(spec)
spec.loader.exec_module(update)

OLD = "sha256:" + "a" * 64
NEW = "sha256:" + "b" * 64
ECR = "public.ecr.aws/docker/library/golang"
GHCR = "mirror.gcr.io/library/python"


def entry(dockerfile: str, repo: str, tag: str) -> dict:
    return {
        "dockerfile": dockerfile,
        "alias": f"{repo}:{tag}",
        "resolved_repo": repo,
        "resolved_tag": tag,
        "digest": OLD,
        "pinned_in_dockerfile": f"FROM {repo}:{tag}@{OLD}",
    }


class RetryTest(unittest.TestCase):
    def test_a_transient_failure_is_retried(self) -> None:
        answers = iter([None, NEW])
        slept: list[int] = []
        with mock.patch.object(update, "resolve_digest", side_effect=lambda *a: next(answers)):
            got = update.resolve_digest_with_retry(ECR, "1.26.10", "linux/amd64", sleep=slept.append)
        self.assertEqual(got, NEW)
        self.assertEqual(slept, [update.RESOLVE_BACKOFF_SECONDS[0]])

    def test_gives_up_after_every_attempt(self) -> None:
        calls: list[str] = []
        slept: list[int] = []
        with mock.patch.object(update, "resolve_digest", side_effect=lambda *a: calls.append(a[0])):
            got = update.resolve_digest_with_retry(ECR, "1.26.10", "linux/amd64", sleep=slept.append)
        self.assertIsNone(got)
        self.assertEqual(len(calls), update.RESOLVE_ATTEMPTS)
        self.assertEqual(slept, list(update.RESOLVE_BACKOFF_SECONDS))


class PartialTest(unittest.TestCase):
    def setUp(self) -> None:
        self.root = Path(tempfile.mkdtemp())
        (self.root / "images").mkdir()
        self.lock_path = self.root / "images" / "Dockerfile.lock"
        pinned = [
            entry("images/ci-runner.Dockerfile", ECR, "1.26.10"),
            entry("images/runner-python312.Dockerfile", GHCR, "3.12-slim-trixie"),
        ]
        self.lock_path.write_text(json.dumps({"generated_at": "2026-10-01", "pinned": pinned}, indent=2) + "\n")
        for e in pinned:
            (self.root / e["dockerfile"]).write_text(e["pinned_in_dockerfile"] + "\n")
        self.unresolved = self.root / "unresolved.txt"
        sleeper = mock.patch.object(update.time, "sleep")
        sleeper.start()
        self.addCleanup(sleeper.stop)

    def run_update(self, answers: dict[str, str | None], allow_partial: bool) -> int:
        with mock.patch.object(update, "resolve_digest", side_effect=lambda repo, *a: answers[repo]):
            return update.update_lock_and_dockerfiles(self.root, False, allow_partial, self.unresolved)

    def dockerfile(self, name: str) -> str:
        return (self.root / "images" / name).read_text()

    def test_partial_writes_what_resolved_and_lists_the_rest(self) -> None:
        rc = self.run_update({ECR: None, GHCR: NEW}, allow_partial=True)
        self.assertEqual(rc, 0)
        lock = json.loads(self.lock_path.read_text())
        self.assertEqual([e["digest"] for e in lock["pinned"]], [OLD, NEW])
        self.assertIn(NEW, self.dockerfile("runner-python312.Dockerfile"))
        self.assertIn(OLD, self.dockerfile("ci-runner.Dockerfile"))
        lines = self.unresolved.read_text().splitlines()
        self.assertEqual(len(lines), 1)
        self.assertIn(f"could not resolve {ECR}:1.26.10", lines[0])

    def test_without_partial_an_unresolved_tag_writes_nothing(self) -> None:
        rc = self.run_update({ECR: None, GHCR: NEW}, allow_partial=False)
        self.assertEqual(rc, 1)
        lock = json.loads(self.lock_path.read_text())
        self.assertEqual([e["digest"] for e in lock["pinned"]], [OLD, OLD])
        self.assertIn(OLD, self.dockerfile("runner-python312.Dockerfile"))

    def test_partial_still_fails_when_nothing_resolves(self) -> None:
        rc = self.run_update({ECR: None, GHCR: None}, allow_partial=True)
        self.assertEqual(rc, 1)
        self.assertEqual(len(self.unresolved.read_text().splitlines()), 2)


if __name__ == "__main__":
    unittest.main()
