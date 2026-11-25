import importlib.util
import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch


spec = importlib.util.spec_from_file_location("commit_report", Path(__file__).with_name("commit_acceptance_report.py"))
commit_report = importlib.util.module_from_spec(spec)
spec.loader.exec_module(commit_report)


class CommitReportTest(unittest.TestCase):
    def test_only_failure_counts_are_retained(self):
        output = [
            '{"Action":"fail","Test":"secret-test","Output":"postgres://user:password@host/db"}',
            '{"Action":"skip","Test":"another-secret"}',
            '{"Action":"fail","Package":"secret-package"}',
            'ordinary output containing password',
            '{"Action":"pass","Test":"passing-test"}',
        ]
        self.assertEqual(commit_report.failure_counts(output), {
            "failed_tests": 1, "skipped_tests": 1, "failed_packages": 1,
        })

    def test_gate_status_and_output_sanitization(self):
        for exit_code, status in ((0, "passed"), (1, "failed")):
            with self.subTest(status=status), tempfile.TemporaryDirectory() as temp:
                path = Path(temp) / "report.json"
                commit_report.write_report(path, {"status": "not_started", "source_revision": "abc", "gate": "make test-commit"})

                def fake_run(command, stdout, **kwargs):
                    self.assertEqual(command, ["make", "test-commit"])
                    self.assertEqual(json.loads(path.read_text())["status"], "running")
                    stdout.write('{"Action":"fail","Test":"private-name","Output":"password"}\n')
                    stdout.write('# tests 1\n# pass 1\n# skipped 0\n# todo 0\nRan 1 test in 0.01s\n\nOK\n')
                    return type("Result", (), {"returncode": exit_code})()

                with patch.object(commit_report.subprocess, "run", side_effect=fake_run):
                    self.assertEqual(commit_report.run(path), exit_code)
                raw = path.read_text()
                report = json.loads(raw)
                self.assertEqual(report["status"], status)
                self.assertEqual(report["source_revision"], "abc")
                self.assertNotIn("password", raw)
                self.assertNotIn("private-name", raw)

    def test_sdk_skip_or_missing_summary_fails_closed(self):
        passing = '# tests 1\n# pass 1\n# skipped 0\n# todo 0\nRan 1 test in 0.01s\nOK\n'
        self.assertTrue(commit_report.sdk_ran_without_skips(passing.splitlines()))
        self.assertFalse(commit_report.sdk_ran_without_skips(passing.replace('# skipped 0', '# skipped 1').splitlines()))
        self.assertFalse(commit_report.sdk_ran_without_skips(passing.replace('OK', 'OK (skipped=1)').splitlines()))
        self.assertFalse(commit_report.sdk_ran_without_skips([]))
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / 'report.json'
            commit_report.write_report(path, {"status": "not_started", "gate": "make test-commit"})
            with patch.object(commit_report.subprocess, "run", return_value=type("Result", (), {"returncode": 0})()):
                self.assertEqual(commit_report.run(path), 1)
            self.assertEqual(json.loads(path.read_text())["sdk_skip_check"], "failed")

    def test_consumer_load_report_only_retains_bounded_summary(self):
        valid = "commit_consumer_e2e_test.go:223: sustained Commit consumer load passed: duration=1m5s rate=1 events=68\n"
        self.assertEqual(commit_report.consumer_load_summary([valid]), {
            "configured_duration_seconds": 65, "target_events_per_second": 1, "verified_events": 68,
        })
        self.assertIsNone(commit_report.consumer_load_summary([valid, valid]))
        self.assertIsNone(commit_report.consumer_load_summary([valid.replace("events=68", "events=999999")]))
        self.assertIsNone(commit_report.consumer_load_summary([valid.replace("duration=1m5s", "duration=0m5s")]))
        self.assertIsNone(commit_report.consumer_load_summary([valid.replace("rate=1", "rate=6")]))
        self.assertEqual(commit_report.consumer_load_summary([valid.replace("duration=1m5s", "duration=1m5.5s")])["configured_duration_seconds"], 65.5)
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / "report.json"
            commit_report.write_report(path, {"status": "not_started", "gate": "make test-commit-consumer-load"})

            def successful(command, stdout, **kwargs):
                self.assertEqual(command, ["make", "test-commit-consumer-load"])
                stdout.write("postgres://user:password@host/private\n" + valid)
                return type("Result", (), {"returncode": 0})()

            with patch.object(commit_report.subprocess, "run", side_effect=successful):
                self.assertEqual(commit_report.run(path, "consumer-load"), 0)
            report = json.loads(path.read_text())
            self.assertEqual(report["status"], "passed")
            self.assertEqual(report["load_summary_check"], "passed")
            self.assertEqual(report["consumer_load"]["verified_events"], 68)
            self.assertNotIn("password", path.read_text())
            self.assertNotIn("private", path.read_text())
            with self.assertRaises(ValueError):
                commit_report.run(path, "process")

    def test_consumer_load_missing_summary_and_failed_gate_fail_closed(self):
        for exit_code in (0, 1):
            with self.subTest(exit_code=exit_code), tempfile.TemporaryDirectory() as temp:
                path = Path(temp) / "report.json"
                commit_report.write_report(path, {"status": "not_started", "gate": "make test-commit-consumer-load"})

                def fake_run(command, stdout, **kwargs):
                    stdout.write('{"Action":"skip","Test":"private-test","Output":"password"}\n')
                    return type("Result", (), {"returncode": exit_code})()

                with patch.object(commit_report.subprocess, "run", side_effect=fake_run):
                    self.assertEqual(commit_report.run(path, "consumer-load"), 1)
                raw = path.read_text()
                report = json.loads(raw)
                self.assertEqual(report["status"], "failed")
                self.assertEqual(report["failure_event_counts"]["skipped_tests"], 1)
                self.assertEqual(report["load_summary_check"], "failed" if exit_code == 0 else "not_checked")
                self.assertNotIn("private-test", raw)
                self.assertNotIn("password", raw)

        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / "report.json"
            commit_report.write_report(path, {"gate": "make test-commit-consumer-load"})

            def skipped_with_summary(command, stdout, **kwargs):
                stdout.write('{"Action":"skip","Test":"private-test"}\n')
                stdout.write('sustained Commit consumer load passed: duration=1m5s rate=1 events=68\n')
                return type("Result", (), {"returncode": 0})()

            with patch.object(commit_report.subprocess, "run", side_effect=skipped_with_summary):
                self.assertEqual(commit_report.run(path, "consumer-load"), 1)
            self.assertEqual(json.loads(path.read_text())["status"], "failed")

    def test_clean_source_identity_and_dirty_rejection(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            subprocess.run(["git", "init", "-q", str(root)], check=True)
            subprocess.run(["git", "-C", str(root), "config", "user.email", "test@example.invalid"], check=True)
            subprocess.run(["git", "-C", str(root), "config", "user.name", "Test"], check=True)
            (root / "source").write_text("fixture")
            subprocess.run(["git", "-C", str(root), "add", "source"], check=True)
            subprocess.run(["git", "-C", str(root), "commit", "-qm", "fixture"], check=True)
            old = Path.cwd()
            try:
                os.chdir(root)
                with tempfile.TemporaryDirectory() as evidence:
                    report_path = Path(evidence) / "report.json"
                    commit_report.prepare(report_path)
                    report = json.loads(report_path.read_text())
                    self.assertEqual(len(report["source_revision"]), 40)
                    self.assertEqual(len(report["source_archive_sha256"]), 64)
                    self.assertEqual(report["status"], "not_started")
                    commit_report.prepare(report_path, "consumer-load")
                    consumer = json.loads(report_path.read_text())
                    self.assertEqual(consumer["source_revision"], report["source_revision"])
                    self.assertEqual(consumer["source_archive_sha256"], report["source_archive_sha256"])
                    self.assertEqual(consumer["gate"], "make test-commit-consumer-load")
                    (root / "source").write_text("changed")
                    with self.assertRaises(RuntimeError):
                        commit_report.prepare(report_path)
            finally:
                os.chdir(old)

    def test_consumer_load_cli_records_tested_revision(self):
        script = Path(__file__).resolve().with_name("commit_acceptance_report.py")
        with tempfile.TemporaryDirectory() as temp:
            base = Path(temp)
            root = base / "checkout"
            root.mkdir()
            subprocess.run(["git", "init", "-q", str(root)], check=True)
            subprocess.run(["git", "-C", str(root), "config", "user.email", "test@example.invalid"], check=True)
            subprocess.run(["git", "-C", str(root), "config", "user.name", "Test"], check=True)
            (root / "source").write_text("fixture")
            subprocess.run(["git", "-C", str(root), "add", "source"], check=True)
            subprocess.run(["git", "-C", str(root), "commit", "-qm", "fixture"], check=True)
            expected = subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"], text=True).strip()
            fake_make = base / "make"
            fake_make.write_text("#!/bin/sh\n[ \"$1\" = test-commit-consumer-load ] || exit 2\nprintf '%s\\n' 'sustained Commit consumer load passed: duration=1m5s rate=1 events=68'\n")
            fake_make.chmod(0o755)
            env = dict(os.environ, PATH=str(base) + os.pathsep + os.environ["PATH"])
            report_path = base / "evidence" / "report.json"
            for phase in ("prepare", "run"):
                subprocess.run([sys.executable, str(script), phase, str(report_path), "consumer-load"], cwd=root, env=env, check=True, capture_output=True, text=True)
            report = json.loads(report_path.read_text())
            self.assertEqual(report["source_revision"], expected)
            self.assertEqual(len(report["source_archive_sha256"]), 64)
            self.assertEqual(report["status"], "passed")
            self.assertEqual(report["consumer_load"]["verified_events"], 68)


if __name__ == "__main__":
    unittest.main()
