"""Checks that native rollout receipts cannot be promoted from blocked evidence."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from contextlib import redirect_stdout
from io import StringIO
from types import SimpleNamespace

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("recorder", ROOT / "scripts/ops/mcp-qualification.py")
RECORDER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(RECORDER)


class QualificationRecorderTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.commit = "a" * 40
        checks = [
            {"name": name, "status": "pending", "target": ""}
            for name in sorted(RECORDER.CHECKER.REQUIRED)
        ]
        (self.root / "manifest.json").write_text(json.dumps({"version": 1, "commit": self.commit, "checks": checks}))

    def write_report(self, **overrides):
        report = {
            "version": 1,
            "scope": "native-mcp-hosting-rollout",
            "reviewed_commit": self.commit,
            "scenario": "rollout",
            "status": "passed",
            "native_observations": True,
            "provenance": {
                "source_revision": self.commit,
                "binary_revision": self.commit,
                "binary_modified": False,
            },
            "binary_sha256": "b" * 64,
            "plan_sha256": "c" * 64,
        }
        report.update(overrides)
        (self.root / "report.json").write_text(json.dumps(report))

    def record(self, status="passed"):
        args = SimpleNamespace(
            directory=self.root,
            name="tasks_restart_replicas",
            target="dedicated native qualification account",
            artifact="report.json",
            status=status,
        )
        with redirect_stdout(StringIO()):
            RECORDER.record(args)
        return RECORDER.load_manifest(self.root)[2]

    def test_passed_native_receipt_requires_valid_observation_and_provenance(self):
        self.write_report()
        manifest = self.record()
        record = next(row for row in manifest["checks"] if row["name"] == "tasks_restart_replicas")
        self.assertEqual(record["status"], "passed")
        for overrides, expected in (
            ({"status": "blocked", "native_observations": False}, "did not pass"),
            ({"reviewed_commit": "d" * 40}, "commit does not match"),
            ({"provenance": {"source_revision": self.commit, "binary_revision": "d" * 40, "binary_modified": False}}, "provenance"),
            ({"provenance": {"source_revision": self.commit, "binary_revision": self.commit, "binary_modified": True}}, "provenance"),
            ({"binary_sha256": "bad"}, "binary_sha256"),
        ):
            self.write_report(**overrides)
            before = json.loads((self.root / "manifest.json").read_text())
            with self.subTest(overrides=overrides), self.assertRaisesRegex(ValueError, expected):
                self.record()
            self.assertEqual(json.loads((self.root / "manifest.json").read_text()), before)

    def test_failed_native_receipt_remains_recordable(self):
        self.write_report(status="blocked", native_observations=False, provenance=None)
        manifest = self.record(status="failed")
        record = next(row for row in manifest["checks"] if row["name"] == "tasks_restart_replicas")
        self.assertEqual(record["status"], "failed")

    def test_non_native_receipts_keep_manual_review_flow(self):
        (self.root / "report.json").write_text(json.dumps({"observation": "provider login receipt"}))
        manifest = self.record()
        record = next(row for row in manifest["checks"] if row["name"] == "tasks_restart_replicas")
        self.assertEqual(record["status"], "passed")


if __name__ == "__main__":
    unittest.main()
