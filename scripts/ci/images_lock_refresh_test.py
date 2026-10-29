#!/usr/bin/env python3
"""Tests for images_lock_refresh.py.

Run with:
  python3 scripts/ci/images_lock_refresh_test.py

Each test builds a temp repo with a lock, a bare `origin` and a fake `gh`
that logs its arguments, then runs the script the way the workflow does.
Pure stdlib, plain unittest.
"""
from __future__ import annotations

import json
import os
import stat
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent / "images_lock_refresh.py"
OLD = "sha256:" + "a" * 64
NEW = "sha256:" + "b" * 64


def git(cwd: Path, *args: str) -> str:
    return subprocess.run(["git", *args], cwd=cwd, check=True, text=True,
                          capture_output=True).stdout


def lock(digest: str, generated_at: str) -> str:
    entry = {
        "dockerfile": "images/runner-python312.Dockerfile",
        "alias": "mirror.gcr.io/library/python:3.12-slim-trixie",
        "digest": digest,
    }
    # Two entries share one base, as builder-base's do; the issue lists it once.
    return json.dumps({"generated_at": generated_at, "pinned": [entry, dict(entry)]}, indent=2) + "\n"


class RefreshTest(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = Path(tempfile.mkdtemp())
        self.origin = self.tmp / "origin.git"
        self.repo = self.tmp / "repo"
        git(self.tmp, "init", "--quiet", "--bare", str(self.origin))
        git(self.tmp, "init", "--quiet", "-b", "main", str(self.repo))
        (self.repo / "images").mkdir()
        (self.repo / "images" / "Dockerfile.lock").write_text(lock(OLD, "2026-10-01"))
        (self.repo / "images" / "runner-python312.Dockerfile").write_text(f"FROM python@{OLD}\n")
        (self.repo / "images" / "notes.txt").write_text("committed\n")
        git(self.repo, "add", ".")
        git(self.repo, "-c", "user.name=t", "-c", "user.email=t@example.com",
            "commit", "--quiet", "-m", "base")
        git(self.repo, "remote", "add", "origin", str(self.origin))
        bin_dir = self.tmp / "bin"
        bin_dir.mkdir()
        self.gh_log = self.tmp / "gh.log"
        self.issues = self.tmp / "issues.json"
        self.issues.write_text("[]")
        fake_gh = bin_dir / "gh"
        fake_gh.write_text(
            "#!/bin/sh\n"
            # One log line per call, so a multi-line --body stays with its call.
            f"printf '%s' \"$*\" | tr '\\n' ' ' >> {self.gh_log}; echo >> {self.gh_log}\n"
            f"if [ \"$1 $2\" = 'issue list' ]; then cat {self.issues}; fi\n"
        )
        fake_gh.chmod(fake_gh.stat().st_mode | stat.S_IEXEC)
        self.env = dict(os.environ, PATH=f"{bin_dir}:{os.environ['PATH']}",
                        GITHUB_REPOSITORY="owner/repo", GITHUB_RUN_ID="42",
                        IMAGES_LOCK_REFRESH_DATE="20261026")

    def refresh(self, *args: str) -> subprocess.CompletedProcess:
        return subprocess.run([sys.executable, str(SCRIPT), "--repo-root", str(self.repo), *args],
                              env=self.env, capture_output=True, text=True)

    def gh_calls(self) -> list[str]:
        return self.gh_log.read_text().splitlines() if self.gh_log.exists() else []

    def test_no_drift_restores_the_stamp_and_pushes_nothing(self) -> None:
        (self.repo / "images" / "Dockerfile.lock").write_text(lock(OLD, "2026-10-26"))
        (self.repo / "images" / "notes.txt").write_text("someone else's edit\n")
        result = self.refresh()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("every pinned base is current", result.stdout)
        # The stamp is restored; files the resolver does not own are left alone.
        self.assertEqual(git(self.repo, "status", "--porcelain").strip(), "M images/notes.txt")
        self.assertEqual(git(self.origin, "branch", "--list"), "")
        self.assertEqual(self.gh_calls(), [])

    def test_drift_pushes_a_branch_and_opens_one_issue(self) -> None:
        (self.repo / "images" / "Dockerfile.lock").write_text(lock(NEW, "2026-10-26"))
        (self.repo / "images" / "runner-python312.Dockerfile").write_text(f"FROM python@{NEW}\n")
        (self.repo / "images" / "notes.txt").write_text("someone else's edit\n")
        result = self.refresh()
        self.assertEqual(result.returncode, 0, result.stderr)
        branch = "automation/images-lock-refresh-20261026-42"
        self.assertIn(branch, git(self.origin, "branch", "--list"))
        changed = git(self.origin, "show", "--name-only", "--format=", branch).split()
        self.assertEqual(changed, ["images/Dockerfile.lock", "images/runner-python312.Dockerfile"])
        subject = git(self.origin, "log", "-1", "--format=%s", branch).strip()
        self.assertEqual(subject, "build(images): refresh pinned base image digests")
        self.assertFalse(subject.startswith("fix"))
        create = [c for c in self.gh_calls() if c.startswith("issue create")]
        self.assertEqual(len(create), 1)
        self.assertIn("Base image digests drifted", create[0])
        self.assertIn(f"compare/main...{branch}", create[0])
        self.assertEqual(create[0].count("python:3.12-slim-trixie"), 1)
        self.assertIn("aaaaaaaaaaaa", create[0])
        self.assertIn("bbbbbbbbbbbb", create[0])

    def test_drift_comments_on_the_open_issue(self) -> None:
        self.issues.write_text(json.dumps([
            {"number": 7, "title": "Base image digests drifted 2"},
            {"number": 9, "title": "Base image digests drifted"},
        ]))
        (self.repo / "images" / "Dockerfile.lock").write_text(lock(NEW, "2026-10-26"))
        result = self.refresh()
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.gh_calls()
        self.assertFalse([c for c in calls if c.startswith("issue create")])
        self.assertTrue([c for c in calls if c.startswith("issue comment 9 ")])

    def test_dry_run_changes_nothing(self) -> None:
        (self.repo / "images" / "Dockerfile.lock").write_text(lock(NEW, "2026-10-26"))
        result = self.refresh("--dry-run")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("would push", result.stdout)
        self.assertEqual(git(self.origin, "branch", "--list"), "")
        self.assertEqual(self.gh_calls(), [])


if __name__ == "__main__":
    unittest.main()
