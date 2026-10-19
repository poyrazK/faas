#!/usr/bin/env python3
"""Portable negative evidence and orchestration tests; no native claim or host I/O."""
import datetime as dt
import importlib.util
import hashlib
import json
from io import StringIO
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch


def load(name):
    spec = importlib.util.spec_from_file_location(name.replace("-", "_"), Path(__file__).with_name(name + ".py"))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


lane = load("binding-native-lane")
SOURCE = "a" * 40
NOW = dt.datetime(2026, 10, 17, 12, tzinfo=dt.UTC)


class EvidenceTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.out = Path(self.tmp.name) / "evidence"
        self.out.mkdir()
        (self.out / "public").mkdir()
        self.events = {}
        for index, suite in enumerate(lane.SUITES):
            fixture = load(Path(suite["verdict"]).stem + "_test").VerdictTests()
            events = [json.loads(e) if isinstance(e, str) else e for e in fixture.events()]
            for n, event in enumerate(events):
                event["Time"] = (NOW - dt.timedelta(hours=1) + dt.timedelta(minutes=index * 5, seconds=n)).isoformat()
                event["Package"] = "github.com/onebox-faas/faas/cmd/e2e"
                text = event.get("Output", "")
                marker = "GUEST_IDENTITY "
                if marker in text:
                    prefix, raw = text.split(marker, 1)
                    identity = json.loads(raw)
                    manifest = identity.get("runtime") if isinstance(identity.get("runtime"), dict) else identity
                    manifest["source_revision"] = SOURCE
                    for key in lane.SHARED:
                        manifest["artifacts"][key] = "b" * 64
                    event["Output"] = prefix + marker + json.dumps(identity) + "\n"
            self.events[suite["name"]] = events
            self.write(suite)

    def write(self, suite):
        data = "\n".join(json.dumps(e) for e in self.events[suite["name"]]).encode()
        (self.out / (suite["name"] + ".jsonl")).write_bytes(data)
        report = lane.load_verdict(suite).make_report(data, "native", True, True, True, 86400)
        (self.out / (suite["name"] + ".report.json")).write_text(json.dumps(report))

    def verify(self, **kwargs):
        return lane.verify_reports(self.out, SOURCE, now=kwargs.get("now", NOW))

    def test_complete_matrix_is_commit_bound_and_separate_from_release_approval(self):
        report = self.verify()
        self.assertEqual(report["version"], 2)
        self.assertTrue(report["native_guest_qualified"])
        self.assertFalse(report["release_qualified"])
        self.assertEqual([s["name"] for s in report["suites"]], ["postgres", "storage", "queue", "service"])
        self.assertEqual(report["source_revision"], SOURCE)
        self.assertEqual(set(report["shared_artifacts"]), set(lane.SHARED))
        self.assertNotIn("Output", json.dumps(report))
        self.assertNotIn("private.log", json.dumps(report))
        self.assertEqual(list((self.out / "public").iterdir()), [])
        lane.verify_reports(self.out, SOURCE, now=NOW, publish=True)
        self.assertEqual(len(list((self.out / "public").glob("*.json"))), 4)
        for suite in report["suites"]:
            self.assertEqual(suite["report_sha256"], hashlib.sha256((self.out / "public" / suite["report"]).read_bytes()).hexdigest())

    def test_legacy_three_suite_inventory_cannot_qualify(self):
        with patch.object(lane,"SUITES",lane.SUITES[:3]), self.assertRaisesRegex(ValueError,"suite_inventory_incomplete"):
            self.verify()

    def test_every_report_and_private_execution_record_is_required(self):
        for suite in lane.SUITES:
            for path in (self.out / (suite["name"] + ".jsonl"), self.out / (suite["name"] + ".report.json")):
                saved = path.read_bytes()
                path.unlink()
                with self.subTest(path=path.name), self.assertRaises(OSError):
                    self.verify()
                path.write_bytes(saved)

    def test_changed_report_skip_failure_or_missing_cleanup_cannot_pass(self):
        suite = lane.SUITES[2]
        path = self.out / "queue.report.json"
        report = json.loads(path.read_text())
        for field in ("source_clean", "leakcheck_before", "leakcheck_after", "native_qualified"):
            with self.subTest(field=field), self.assertRaises(ValueError):
                path.write_text(json.dumps(dict(report, **{field: False})))
                self.verify()
        path.write_text(json.dumps(report))
        for action in ("skip", "fail"):
            data = self.out / "queue.jsonl"
            saved = data.read_bytes()
            events = json.loads(json.dumps(self.events["queue"]))
            events[3]["Action"] = action
            data.write_text("\n".join(json.dumps(e) for e in events))
            with self.subTest(action=action), self.assertRaises(ValueError):
                self.verify()
            data.write_bytes(saved)
        events = self.events["queue"]
        self.events["queue"] = [e for e in events if "GUEST_IDENTITY " not in e.get("Output", "")]
        with self.assertRaises(ValueError):
            self.write(suite)

    def test_freshness_and_overlap(self):
        for when in (NOW - dt.timedelta(days=1), NOW + dt.timedelta(days=2)):
            with self.subTest(when=when), self.assertRaises(ValueError):
                self.verify(now=when)
        for e in self.events["queue"]:
            e["Time"] = (dt.datetime.fromisoformat(e["Time"]) - dt.timedelta(minutes=10)).isoformat()
        self.write(lane.SUITES[2])
        with self.assertRaisesRegex(ValueError, "overlapping"):
            self.verify()

    def test_other_revision_and_shared_artifact_changes_are_rejected(self):
        suite = lane.SUITES[2]
        event = next(e for e in self.events["queue"] if "GUEST_IDENTITY " in e.get("Output", ""))
        prefix, raw = event["Output"].split("GUEST_IDENTITY ", 1)
        original = json.loads(raw)
        for change in ("revision", *lane.SHARED):
            identity = json.loads(json.dumps(original))
            if change == "revision":
                identity["source_revision"] = "c" * 40
            else:
                identity["artifacts"][change] = "d" * 64
            event["Output"] = prefix + "GUEST_IDENTITY " + json.dumps(identity) + "\n"
            self.write(suite)
            with self.subTest(change=change), self.assertRaises(ValueError):
                self.verify()

    def test_evidence_symlinks_and_unknown_source_are_rejected(self):
        path = self.out / "queue.report.json"
        saved = path.read_bytes()
        path.unlink()
        target = self.out / "original"
        target.write_bytes(saved)
        path.symlink_to(target)
        with self.assertRaisesRegex(ValueError, "symlink"):
            self.verify()
        with self.assertRaisesRegex(ValueError, "source_revision_invalid"):
            lane.verify_reports(self.out, "main", now=NOW)

    def test_runner_stops_on_failure_and_preserves_private_output(self):
        calls = []

        def fake_run(command, **kwargs):
            calls.append(command[-1])
            kwargs["stdout"].write("private fixture password must stay on host\n")
            return subprocess.CompletedProcess(command, 1)

        out = self.out.parent / "failed"
        with patch.object(lane.subprocess, "check_output", side_effect=[(SOURCE + "\n"), b""]), patch.object(lane.subprocess, "run", side_effect=fake_run):
            with self.assertRaisesRegex(ValueError, "postgres_journey_failed"):
                lane.run(SOURCE, out, "go")
        self.assertEqual(calls, ["test-managed-postgres-native"])
        self.assertFalse((out / "public/summary.json").exists())
        self.assertIn("password", (out / "postgres.private.log").read_text())
        with patch.object(lane.subprocess, "check_output", side_effect=[SOURCE + "\n", b""]):
            with self.assertRaises(FileExistsError):
                lane.run(SOURCE, out, "go")

    def test_orchestrator_runs_all_four_with_separate_private_receipts(self):
        calls = []
        out = self.out.parent / "complete"
        original_verify = lane.verify_reports

        def fake_run(command, **kwargs):
            suite = lane.SUITES[len(calls)]
            calls.append(command[-1])
            env = kwargs["env"]
            self.assertEqual(env[suite["env_prefix"] + "_HOST_CLASS"], "native")
            self.assertNotIn("postgres://", " ".join(command))
            Path(env[suite["env_prefix"] + "_RESULTS_PATH"]).write_bytes((self.out / (suite["name"] + ".jsonl")).read_bytes())
            Path(env[suite["env_prefix"] + "_REPORT_PATH"]).write_bytes((self.out / (suite["name"] + ".report.json")).read_bytes())
            return subprocess.CompletedProcess(command, 0)

        def verify(directory, source, publish):
            return original_verify(directory, source, now=NOW, publish=publish)

        with patch.object(lane.subprocess, "check_output", side_effect=[SOURCE + "\n", b"", SOURCE + "\n", b""]), patch.object(lane.subprocess, "run", side_effect=fake_run), patch.object(lane, "verify_reports", side_effect=verify), patch("sys.stdout", new=StringIO()) as output:
            lane.run(SOURCE, out, "pinned-go")
        self.assertIn("all four guest journeys and cleanup passed", output.getvalue())
        self.assertEqual(calls, [suite["target"] for suite in lane.SUITES])
        self.assertEqual(len(list((out / "public").glob("*.json"))), 5)
        self.assertTrue(json.loads((out / "public/summary.json").read_text())["native_guest_qualified"])


class SourceBundleTests(unittest.TestCase):
    def test_clean_commit_round_trip_has_git_and_no_checkout_credentials(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            source = root / "repo"
            source.mkdir()
            def git(*args):
                return subprocess.check_output(["git", "-C", str(source), *args], stderr=subprocess.PIPE, text=True).strip()
            git("init", "-q")
            git("config", "user.name", "Fixture")
            git("config", "user.email", "fixture@example.invalid")
            git("config", "http.https://github.com/.extraheader", "fixture-sensitive-header")
            (source / "tracked").write_text("source\n")
            (source / "go.mod").write_text("module example.invalid/native-source-fixture\n\ngo 1.23\n")
            (source / "main.go").write_text("package main\nfunc main() {}\n")
            git("add", "tracked", "go.mod", "main.go")
            git("commit", "-qm", "fixture")
            sha = git("rev-parse", "HEAD")
            hook = source / ".git/hooks/post-checkout"
            hook.write_text("#!/bin/sh\nexit 91\n")
            hook.chmod(0o700)
            script = lane.REPO / "scripts/ci/package-binding-native-source.sh"
            archive = root / "source.tar.gz"
            result = subprocess.run(["bash", str(script), sha, str(archive)], cwd=source, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            subprocess.run(["tar", "-xzf", str(archive), "-C", str(root)], check=True)
            self.assertTrue((root / "source/.git").is_dir())
            config = (root / "source/.git/config").read_text()
            self.assertNotIn("extraheader", config)
            self.assertNotIn("fixture-sensitive-header", config)
            actual = subprocess.check_output(["git", "-C", str(root / "source"), "rev-parse", "HEAD"], text=True).strip()
            self.assertEqual(actual, sha)
            self.assertFalse((root / "source/.git/hooks/post-checkout").exists())
            binary = root / "fixture-bin"
            subprocess.run(["go", "build", "-buildvcs=true", "-o", str(binary), "."], cwd=root / "source", check=True, capture_output=True)
            metadata = subprocess.check_output(["go", "version", "-m", str(binary)], text=True)
            self.assertIn("vcs.revision=" + sha, metadata)
            self.assertIn("vcs.modified=false", metadata)
            # checkout@v7 defaults to depth one; retain its shallow Git graph.
            shallow = root / "shallow"
            subprocess.run(["git", "clone", "--quiet", "--depth=1", source.as_uri(), str(shallow)], check=True, capture_output=True)
            shallow_archive = root / "shallow.tar.gz"
            result = subprocess.run(["bash", str(script), sha, str(shallow_archive)], cwd=shallow, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            restored = root / "restored"
            restored.mkdir()
            subprocess.run(["tar", "-xzf", str(shallow_archive), "-C", str(restored)], check=True)
            self.assertTrue((restored / "source/.git/shallow").is_file())
            self.assertEqual(subprocess.check_output(["git", "-C", str(restored / "source"), "rev-parse", "HEAD"], text=True).strip(), sha)
            self.assertNotEqual(subprocess.run(["bash", str(script), sha, str(archive)], cwd=source, capture_output=True).returncode, 0)
            (source / "tracked").write_text("dirty\n")
            self.assertNotEqual(subprocess.run(["bash", str(script), sha, str(root / "dirty.tar.gz")], cwd=source, capture_output=True).returncode, 0)


if __name__ == "__main__":
    unittest.main()
