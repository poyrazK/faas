#!/usr/bin/env python3
import importlib.util
import json
from pathlib import Path
import unittest


def module(name, filename):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(filename))
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


fixture = module("fixture", "financial-system-verdict_test.py")
checker = module("checker", "financial-system-ci-check.py")


class CollectedEvidenceTest(unittest.TestCase):
    def setUp(self):
        self.case = fixture.VerdictTest()
        self.case.setUp()
        self.addCleanup(self.case.doCleanups)
        self.args = self.case.args
        self.report = self.case.report()
        self.path = self.args.root / f"financial-system-verdict-{self.args.run_id}.json"
        self.path.write_text(json.dumps(self.report))

    def verify(self):
        return checker.verify(self.args.root, self.args.root, self.args.source_sha, self.args.run_id)

    def test_exported_logs_vectors_and_source_match(self):
        self.assertTrue(self.verify()["qualified"])

    def test_stale_commit_run_or_changed_source_fails(self):
        for key, value in (("source_sha", "b" * 40), ("run_id", "stale"), ("qualified", False)):
            self.path.write_text(json.dumps({**self.report, key: value}))
            with self.assertRaises(ValueError):
                self.verify()
        self.path.write_text(json.dumps(self.report))
        source = self.args.root / fixture.verdict.SOURCES["native"]
        source.write_text(source.read_text() + "\n// changed reviewed source\n")
        with self.assertRaises(ValueError):
            self.verify()

    def test_changed_or_missing_native_logs_fail(self):
        self.args.native_log.write_text(self.args.native_log.read_text() + "changed\n")
        with self.assertRaises(ValueError):
            self.verify()
        self.args.native_log.unlink()
        with self.assertRaises(ValueError):
            self.verify()

    def test_changed_remote_source_or_rehashed_false_measurements_fail(self):
        path = self.args.root / fixture.verdict.REMOTE_SUPPORT[1]
        original = path.read_text()
        path.write_text(original + "changed routing\n")
        with self.assertRaises(ValueError):
            self.verify()
        path.write_text(original)
        self.args.native_log.write_text(self.args.native_log.read_text().replace("private_counter=44000", "private_counter=45000"))
        self.report["lanes"]["native"]["log_sha256"] = fixture.verdict.digest(self.args.native_log)
        self.path.write_text(json.dumps(self.report))
        with self.assertRaises(ValueError):
            self.verify()

    def test_rehashed_billing_vectors_cannot_hide_quantity_mismatch(self):
        path = self.args.root / "financial-system-records/reconciliation-unavailable.json"
        proof = json.loads(path.read_text())
        proof["provider"]["events"]["egress"]["metadata"]["egress_bytes"] = 1
        path.write_text(json.dumps(proof))
        self.report["reconciliation"]["unavailable"]["sha256"] = fixture.verdict.digest(path)
        self.path.write_text(json.dumps(self.report))
        with self.assertRaises(ValueError):
            self.verify()


if __name__ == "__main__":
    unittest.main()
