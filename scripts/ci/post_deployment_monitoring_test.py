#!/usr/bin/env python3
"""Offline transport and workflow contract tests. No real SSH or notifications."""

import datetime as dt
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts/ci/post_deployment_monitoring.py"
RELEASE = "a" * 40
AFTER = (dt.datetime.now(dt.timezone.utc) - dt.timedelta(minutes=2)).strftime("%Y-%m-%dT%H:%M:%SZ")
CHECKS = ("serving_probes", "notification_pipeline", "notification_receivers")


class MonitoringTransportTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.report = self.root / "report.json"
        stub = self.root / "ssh"
        stub.write_text("""#!/usr/bin/env python3
import json, os, pathlib, sys
pathlib.Path(os.environ['CAPTURE']).write_text(json.dumps({'args': sys.argv[1:], 'stdin': sys.stdin.read(), 'secret_in_env': 'MONITORING_OPERATOR_TOKEN' in os.environ}))
print(pathlib.Path(os.environ['RESPONSE_FILE']).read_text() if 'RESPONSE_FILE' in os.environ else os.environ.get('RESPONSE', 'private-provider-error'))
print('private-provider-error', file=sys.stderr)
raise SystemExit(int(os.environ.get('SSH_CODE', '255')))
""")
        stub.chmod(0o700)
        self.env = {"PATH": str(self.root) + os.pathsep + os.environ["PATH"], "DESIRED_RELEASE": RELEASE,
                    "MONITORING_REPORT": str(self.report), "MONITORING_NOT_BEFORE": AFTER,
                    "MONITORING_OPERATOR_TOKEN": "fixture-private-token", "CP_HOST": "cp.example.test",
                    "MONITORING_SSH_KEY": str(self.root / "key"), "GITHUB_STEP_SUMMARY": str(self.root / "summary"),
                    "CAPTURE": str(self.root / "capture.json")}

    def invoke(self, *args):
        process = subprocess.run([sys.executable, str(SCRIPT), *args], env=self.env, capture_output=True, text=True, check=False, timeout=10)
        report = json.loads(self.report.read_text())
        self.assertNotIn("fixture-private-token", self.report.read_text() + process.stdout + process.stderr)
        self.assertNotIn("private-provider-error", self.report.read_text() + process.stdout + process.stderr)
        self.assertEqual(self.report.stat().st_mode & 0o777, 0o600)
        return process, report

    def response(self, state="passed"):
        checks = [{"name": name, "state": "passed", "reason": "", "runbook": "https://github.com/poyrazK/faas/blob/main/docs/ops/post-deployment-monitoring.md"} for name in CHECKS]
        if state != "passed":
            checks[0]["state"], checks[0]["reason"] = state, "serving_probe_fault" if state == "degraded" else "source_unavailable"
        return {"schema_version": 1, "scope": "post_deployment_monitoring", "requested_release": RELEASE,
                "evidence_after": AFTER, "state": state, "reason": "", "attempts": 2,
                "had_confirmed_faults": state == "degraded", "checks": checks}

    def test_initialize_always_replaces_prior_pass(self):
        self.report.write_text(json.dumps(self.response()))
        self.env.pop("MONITORING_NOT_BEFORE")
        result, report = self.invoke("--initialize")
        self.assertEqual(result.returncode, 0)
        self.assertEqual((report["state"], report["reason"]), ("unknown", "verification_not_started"))
        self.assertIsNone(report["evidence_after"])
        self.assertFalse((self.root / "capture.json").exists())

    def test_prerequisites_never_contact_ssh(self):
        for key, value, reason in (("MONITORING_OPERATOR_TOKEN", "", "operator_credential_unavailable"),
                                   ("MONITORING_OPERATOR_TOKEN", "token\nsecond-line", "operator_credential_unavailable"),
                                   ("MONITORING_NOT_BEFORE", "", "rollout_boundary_unavailable"),
                                   ("MONITORING_NOT_BEFORE", "tomorrow", "rollout_boundary_unavailable"),
                                   ("CP_HOST", "-oProxyCommand=unsafe", "ssh_host_unavailable")):
            with self.subTest(key=key, reason=reason):
                original = self.env[key]
                self.env[key] = value
                process, report = self.invoke()
                self.env[key] = original
                self.assertEqual(process.returncode, 3)
                self.assertEqual(report["reason"], reason)
                self.assertFalse((self.root / "capture.json").exists())

    def test_transport_and_old_binary_leave_unknown(self):
        for code, reason in ((255, "verification_unavailable"), (127, "verification_unavailable"), (64, "active_release_changed")):
            with self.subTest(code=code):
                self.env["SSH_CODE"] = str(code)
                process, report = self.invoke()
                self.assertEqual(process.returncode, 3)
                self.assertEqual(report["reason"], reason)

    def test_success_fault_and_unknown_are_retained(self):
        for state, code in (("passed", 0), ("degraded", 1), ("unknown", 3)):
            with self.subTest(state=state):
                self.env.update(SSH_CODE=str(code), RESPONSE=json.dumps(self.response(state)))
                process, report = self.invoke()
                self.assertEqual(process.returncode, code)
                self.assertEqual(report["state"], state)
                capture = json.loads((self.root / "capture.json").read_text())
                self.assertEqual(capture["stdin"], "fixture-private-token\n")
                self.assertFalse(capture["secret_in_env"])
                args = capture["args"]
                self.assertNotIn("fixture-private-token", " ".join(args))
                self.assertIn("StrictHostKeyChecking=yes", args)
                self.assertIn("BatchMode=yes", args)
                self.assertIn("--token-stdin", args[-1])
                self.assertIn("FAAS_APID_URL=http://127.0.0.1:8080", args[-1])
                self.assertEqual(args[-1].count("readlink -f /opt/faas/current"), 2)
                self.assertNotIn("deployctl", args[-1])

    def test_invalid_evidence_cannot_become_green(self):
        samples = []
        for key, value in (("requested_release", "b" * 40), ("evidence_after", "2000-01-01T00:00:00Z"),
                           ("scope", "another_scope"), ("checks", []), ("state", "unknown"),
                           ("had_confirmed_faults", None)):
            response = self.response()
            response[key] = value
            samples.append(json.dumps(response))
        inconsistent = self.response()
        inconsistent["checks"][0]["state"] = "unknown"
        overflow = json.dumps(self.response())[:-1] + ', "value": 1e999}'
        samples += [json.dumps(inconsistent), "private-provider-error", '{"value": NaN}', overflow, "x" * ((1 << 20) + 1)]
        self.env["SSH_CODE"] = "0"
        for index, sample in enumerate(samples):
            with self.subTest(index=index):
                # Large fixtures travel through a file to avoid OS env limits.
                response_file = self.root / "response.json"
                response_file.write_text(sample)
                self.env["RESPONSE_FILE"] = str(response_file)
                process, report = self.invoke()
                self.assertEqual(process.returncode, 3)
                self.assertEqual(report["reason"], "invalid_verification_report")


class WorkflowContractTest(unittest.TestCase):
    def test_platform_monitoring_cannot_trigger_rollback(self):
        workflow = (ROOT / ".github/workflows/cd-platform.yml").read_text()
        recovery = workflow.index("- name: Roll back the platform release on gate failure")
        check = workflow.index("- name: Verify post-deployment monitoring")
        upload = workflow.index("- name: Upload post-deployment monitoring evidence")
        self.assertLess(recovery, check)
        self.assertLess(check, upload)
        self.assertNotIn("if:", workflow[check:upload])  # Default success() only.
        self.assertIn("if: always()", workflow[upload:])
        self.assertIn("secrets.MONITORING_OPERATOR_TOKEN", workflow[check:upload])
        self.assertIn("github.workflow_sha", workflow)
        self.assertLess(workflow.index('echo "ACTIVE_NODE_COUNT='), workflow.index('echo "MONITORING_NOT_BEFORE='))
        self.assertLess(workflow.index('echo "MONITORING_NOT_BEFORE='), recovery)


if __name__ == "__main__":
    unittest.main()
