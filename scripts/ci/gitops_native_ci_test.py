#!/usr/bin/env python3
"""No-host contract tests for the native CI evidence boundary."""
import importlib.util
import json
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
import gitops_native_report as report
import gitops_native_report_test as fixtures

spec = importlib.util.spec_from_file_location("gitops_ci_verdict", ROOT / "scripts/ci/gitops-native-verdict.py")
verdict = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verdict)
SHA = "a" * 40
SECRET = "private-diagnostic-must-not-leak"


class NativeCIContracts(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.directory = Path(self.tmp.name) / "evidence"
        self.private = self.directory / "private"
        self.private.mkdir(mode=0o700, parents=True)
        self.setup = {"phase": "native", "reason": "completed", "exit_code": 0}
        self.write_setup()
        self.make_report()

    def write_setup(self):
        path = self.directory / "setup.json"
        path.write_text(json.dumps(self.setup))
        path.chmod(0o600)

    def make_report(self, events=None, metadata=None, host_class="native"):
        initial = metadata or fixtures.metadata()
        initial["host"]["name"] = SECRET
        value = {"schema": report.SCHEMA, "coverage": report.required_coverage(),
                 "evidence_kind": "local_operator_run", "initial": initial, "host_class": host_class,
                 "production_enforcement_enabled": False}
        report.write_json(self.private / "report.json", value)
        for name in report.ARTIFACTS:
            (self.private / name).write_text(SECRET)
            (self.private / name).chmod(0o600)
        (self.private / "test-events.jsonl").write_text("".join(json.dumps(e) + "\n" for e in (events or fixtures.successful_events())))
        with patch.object(report, "metadata", return_value=initial):
            report.finish(ROOT, self.private, {key: "pass" for key in verdict.STAGES}, "", 0)

    def read_report(self):
        return json.loads((self.private / "report.json").read_text())

    def test_complete_matrix_at_exact_sha_passes(self):
        summary = verdict.verify(self.directory, SHA)
        self.assertTrue(summary["native_acceptance_passed"])
        self.assertTrue(summary["source_scheduled_jobs_passed"])
        self.assertEqual(summary["result"], "passed")
        self.assertEqual(summary["errors"], [])
        self.assertEqual(summary["source_commit"], SHA)
        self.assertEqual(set(summary["tests"]), set(report.required_tests()))

    def test_missing_skipped_or_failed_source_scenarios_cannot_pass(self):
        name = report.SOURCE_JOB_TEST + "/gitlab/dockerfile/held_build_supersession"
        for action in ("missing", "skip", "fail"):
            events = fixtures.successful_events()
            if action == "missing":
                events = [e for e in events if e.get("Test") != name]
            else:
                for event in events:
                    if event.get("Test") == name and event["Action"] == "pass":
                        event["Action"] = action
            self.make_report(events)
            self.assertFalse(verdict.verify(self.directory, SHA)["native_acceptance_passed"], action)

    def test_sha_diagnostic_metadata_and_production_scope_are_checked(self):
        self.assertFalse(verdict.verify(self.directory, "b" * 40)["native_acceptance_passed"])
        self.assertIsNone(verdict.verify(self.directory, "b" * 40)["source_commit"])
        self.make_report(host_class="nested-diagnostic")
        self.assertFalse(verdict.verify(self.directory, SHA)["native_acceptance_passed"])
        for change in (lambda v: v.update(production_enforcement_enabled=True),
                       lambda v: v.update(schema="gregale.dev/gitops-native-acceptance/v1"),
                       lambda v: v["initial"]["base_images"].update(builder="latest")):
            self.make_report()
            value = self.read_report()
            change(value)
            report.write_json(self.private / "report.json", value)
            self.assertFalse(verdict.verify(self.directory, SHA)["native_acceptance_passed"])

    def test_changed_artifacts_and_forged_pass_flags_fail(self):
        for name in report.ARTIFACTS:
            self.make_report()
            with (self.private / name).open("a") as stream:
                stream.write("changed")
            summary = verdict.verify(self.directory, SHA)
            self.assertFalse(summary["native_acceptance_passed"], name)
            self.assertIn("artifact_fingerprints_mismatch", summary["errors"])
        self.make_report()
        value = self.read_report()
        value["events"]["complete"] = False
        report.write_json(self.private / "report.json", value)
        self.assertIn("reported_verdict_mismatch", verdict.verify(self.directory, SHA)["errors"])

    def test_failed_or_interrupted_runner_cannot_borrow_good_evidence(self):
        for outcome in ("failure", "cancelled", "skipped"):
            self.assertFalse(verdict.verify(self.directory, SHA, outcome)["native_acceptance_passed"])
        for phase, reason, code in (("setup", "host_configuration_missing", 1), ("native", "interrupted", 143), ("native", "completed", 1)):
            self.setup = {"phase": phase, "reason": reason, "exit_code": code}
            self.write_setup()
            self.assertFalse(verdict.verify(self.directory, SHA)["native_acceptance_passed"])
        (self.private / "report.json").unlink()
        summary = verdict.verify(self.directory, SHA)
        self.assertFalse(summary["native_acceptance_passed"])
        self.assertIn("native_report_missing_or_invalid", summary["errors"])

    def test_publish_exports_only_safe_fields_and_preserves_private_modes(self):
        value = self.read_report()
        value["unexpected"] = SECRET
        report.write_json(self.private / "report.json", value)
        summary = verdict.verify(self.directory, SHA)
        verdict.publish(self.directory, summary)
        public = self.directory / "public"
        self.assertEqual(set(p.name for p in public.iterdir()), {"summary.json", "summary.md"})
        for path in public.iterdir():
            self.assertNotIn(SECRET, path.read_text())
            self.assertNotIn(str(self.private), path.read_text())
            self.assertEqual(path.stat().st_mode & 0o777, 0o644)
        self.assertEqual(self.private.stat().st_mode & 0o777, 0o700)
        self.assertEqual((self.directory / "setup.json").stat().st_mode & 0o777, 0o600)
        self.assertFalse(summary["production_enforcement_enabled"])

    def test_cli_failure_still_publishes_blocked_summary(self):
        directory = Path(self.tmp.name) / "never-started"
        result = subprocess.run([sys.executable, str(ROOT / "scripts/ci/gitops-native-verdict.py"),
                                 "--directory", str(directory), "--expected-sha", SHA, "--runner-outcome", "skipped"], capture_output=True, text=True)
        self.assertEqual(result.returncode, 1)
        self.assertEqual(json.loads((directory / "public/summary.json").read_text())["result"], "blocked")
        self.assertNotIn(SECRET, result.stdout + result.stderr)

    def test_workflow_keeps_privileged_dispatch_and_timeout_boundaries(self):
        workflow = (ROOT / ".github/workflows/gitops-native.yml").read_text()
        self.assertRegex(workflow, r"(?m)^on:\n  workflow_dispatch:\s*\n")
        for event in ("pull_request", "pull_request_target", "push", "schedule", "workflow_call", "repository_dispatch"):
            self.assertNotRegex(workflow, r"(?m)^  " + event + r":")
        self.assertEqual(workflow.count("if: github.ref == 'refs/heads/main'"), 2)
        self.assertIn("environment: gitops-acceptance", workflow)
        self.assertIn("runs-on: [self-hosted, linux, x64, gregale-gitops-acceptance]", workflow)
        self.assertIn("cancel-in-progress: false", workflow)
        self.assertIn("NATIVE_OUTCOME: ${{ steps.acceptance.outcome }}", workflow)
        native = (ROOT / "scripts/test-environment-gitops-native.sh").read_text()
        test_minutes = int(re.search(r"-timeout (\d+)m", native)[1])
        cleanup, outer = map(int, re.search(r"--kill-after=(\d+)m (\d+)m", workflow).groups())
        job_minutes = max(map(int, re.findall(r"timeout-minutes: (\d+)", workflow)))
        self.assertGreater(outer, test_minutes)
        self.assertGreater(job_minutes, outer + cleanup)
        upload = workflow.split("uses: actions/upload-artifact@", 1)[1]
        paths = re.findall(r"\$\{\{ env.GREGALE_GITOPS_CI_EVIDENCE \}\}/([^\n]+)", upload)
        self.assertEqual(paths, ["public/summary.json", "public/summary.md"])

    def test_wrapper_refuses_to_overwrite_evidence_before_native_effects(self):
        result = subprocess.run(["bash", str(ROOT / "scripts/ci/run-gitops-native-acceptance.sh"), SHA, str(self.directory)], capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.setup, json.loads((self.directory / "setup.json").read_text()))
        self.assertNotIn(SECRET, result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()
