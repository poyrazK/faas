#!/usr/bin/env python3
"""Offline transport and workflow contract tests. No real SSH or notifications."""

import datetime as dt
import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts/ci/post_deployment_monitoring.py"
RELEASE = "a" * 40
AFTER = (dt.datetime.now(dt.UTC) - dt.timedelta(minutes=2)).strftime("%Y-%m-%dT%H:%M:%SZ")
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

    def test_optional_timeline_survives_transport_for_every_exit_state(self):
        # Old v1 responses without this field are covered above. A newer CLI
        # must retain its historical fault evidence even when the final poll
        # passes, or when verification ends degraded/unknown.
        for state, code in (("passed", 0), ("degraded", 1), ("unknown", 3)):
            with self.subTest(state=state):
                response = self.response(state)
                response["had_confirmed_faults"] = True
                response["timeline"] = {
                    "transition_limit": 256, "dropped_transitions": 0, "truncated": False,
                    "transitions": [{"at": AFTER, "attempt": 1, "check": "serving_probes",
                                     "state": "degraded", "reason": "serving_probe_fault"}],
                    "checks": [{"name": "serving_probes", "first_fault_at": AFTER,
                                "first_fault_reason": "serving_probe_fault",
                                "observed_fault_seconds": 1, "unrecovered_fault": state != "passed"}],
                }
                self.env.update(SSH_CODE=str(code), RESPONSE=json.dumps(response))
                process, report = self.invoke()
                self.assertEqual(process.returncode, code)
                self.assertEqual(report["timeline"], response["timeline"])
                self.assertTrue(report["had_confirmed_faults"])
                self.assertIn("Confirmed faults were observed during verification.", (self.root / "summary").read_text())

    def test_fault_histories_survive_recovery_and_lost_current_evidence(self):
        capture = {
            "cause_sha256": "c" * 64, "total": 1, "omitted": 0, "truncated": False,
            "items": [{"node": "fsn-2", "url_id": "b" * 64, "state": "failing", "reason": "probe_failing",
                       "evaluated_at": AFTER, "last_run_at": AFTER,
                       "diagnostics": {"state": "available", "evaluated_at": AFTER, "failure_code": "tls",
                                       "first_failure_code": "tls", "attempts": 2, "http_status": 0,
                                       "recovered_on_retry": False}}],
        }
        notification_capture = {
            "cause_sha256": "d" * 64, "total": 1, "omitted": 0, "truncated": False,
            "items": [{"kind": "receiver", "receiver": "faas-page", "integration": "slack",
                       "reason": "requests_failing", "expected": True,
                       "signals": [{"name": "failures", "state": "healthy", "value": 1.5,
                                    "observed_at": AFTER, "window_seconds": 600, "max_age_seconds": 120}]}],
        }
        for state, code in (("passed", 0), ("unknown", 3)):
            with self.subTest(state=state):
                response = self.response(state)
                response["had_confirmed_faults"] = True
                response["timeline"] = {
                    "transition_limit": 256, "dropped_transitions": 0, "truncated": False,
                    "transitions": [{"at": AFTER, "attempt": 1, "check": "serving_probes",
                                     "state": "degraded", "reason": "serving_probe_fault", "probe_failures": capture},
                                    {"at": AFTER, "attempt": 1, "check": "notification_receivers",
                                     "state": "degraded", "reason": "receiver_requests_failing",
                                     "notification_failures": notification_capture}],
                    "checks": [],
                    "probe_evidence": {"per_transition_limit": 4, "item_limit": 64, "byte_limit": 65536,
                                       "retained_items": 1, "retained_bytes": 1200, "dropped_items": 0,
                                       "dropped_snapshots": 0, "truncated": False},
                    "notification_evidence": {"per_transition_limit": 4, "item_limit": 16, "byte_limit": 8192,
                                              "retained_items": 1, "retained_bytes": 650, "dropped_items": 0,
                                              "dropped_snapshots": 0, "truncated": False},
                }
                self.env.update(SSH_CODE=str(code), RESPONSE=json.dumps(response))
                process, report = self.invoke()
                self.assertEqual(process.returncode, code)
                self.assertEqual(report["timeline"], response["timeline"])


    def test_source_read_history_survives_recovery_and_unknown_evidence(self):
        for state, code in (("passed", 0), ("unknown", 3)):
            with self.subTest(state=state):
                response = self.response(state)
                response["sources"] = {
                    "serving_health": {"state": "succeeded", "http_status": 200, "reads": 2,
                                       "failed_reads": 0, "cancelled_reads": 0, "last_success_at": AFTER},
                    "notification_health": {
                        "state": "succeeded" if code == 0 else "failed",
                        "reason": "" if code == 0 else "server_error",
                        "http_status": 200 if code == 0 else 503,
                        "reads": 2, "failed_reads": 1 if code == 0 else 2, "cancelled_reads": 0,
                        "consecutive_failures": 0 if code == 0 else 2,
                        "first_failure_at": AFTER, "last_read_at": AFTER,
                        "last_success_at": AFTER if code == 0 else None,
                        "last_recovery_at": AFTER if code == 0 else None,
                        "last_failure": {"at": AFTER, "reason": "forbidden" if code == 0 else "server_error",
                                         "http_status": 403 if code == 0 else 503},
                        "last_read_seconds": 0.01,
                    },
                }
                response["timeline"] = {
                    "transition_limit": 256, "dropped_transitions": 0, "truncated": False,
                    "transitions": [{"at": AFTER, "attempt": 1, "check": "notification_pipeline",
                                     "state": "unknown", "reason": "source_forbidden", "source_http_status": 403}],
                    "checks": [],
                }
                self.env.update(SSH_CODE=str(code), RESPONSE=json.dumps(response))
                process, report = self.invoke()
                self.assertEqual(process.returncode, code)
                self.assertEqual(report["sources"], response["sources"])
                self.assertEqual(report["timeline"], response["timeline"])


    def test_qualification_blockers_survive_recovery_and_unavailable_reads(self):
        preview = {
            "total": 1, "omitted": 0, "truncated": False, "item_limit": 4,
            "byte_limit": 4096, "retained_bytes": 600,
            "items": [{"kind": "probe_evaluation", "node": "fsn-2", "url_id": "b" * 64,
                       "reason": "missing_timestamp", "source_state": "unknown", "source_reason": "query_failed",
                       "observed_at": None, "age_seconds": None, "max_age_seconds": 120,
                       "allowed_clock_skew_seconds": 30}],
        }
        for state, code in (("passed", 0), ("unknown", 3)):
            with self.subTest(state=state):
                response = self.response(state)
                response["qualification_evidence"] = {
                    "observed_at": AFTER,
                    "checks": [{"name": "serving_probes", "state": "passed" if code == 0 else "unavailable",
                                "last_blocked": {"at": AFTER, "blockers": preview}},
                               {"name": "notification_pipeline", "state": "passed"},
                               {"name": "notification_receivers", "state": "passed"}],
                }
                self.env.update(SSH_CODE=str(code), RESPONSE=json.dumps(response))
                process, report = self.invoke()
                self.assertEqual(process.returncode, code)
                self.assertEqual(report["qualification_evidence"], response["qualification_evidence"])
                self.assertFalse(report["had_confirmed_faults"])


    def test_qualification_progress_and_truncated_history_survive_transport(self):
        for state, code in (("passed", 0), ("unknown", 3)):
            with self.subTest(state=state):
                response = self.response(state)
                preview = {"total": 1, "omitted": 0, "truncated": False, "item_limit": 4,
                           "byte_limit": 4096, "retained_bytes": 300,
                           "items": [{"kind": "pipeline_signal", "name": "prometheus_queue_length",
                                      "reason": "missing_samples", "observed_at": None, "age_seconds": None}]}
                response["qualification_evidence"] = {
                    "observed_at": AFTER,
                    "checks": [{"name": "notification_pipeline", "state": "passed" if code == 0 else "unavailable",
                                "progress": {"first_observed_at": AFTER, "first_blocked_at": AFTER,
                                             "first_passed_at": AFTER if code == 0 else None, "state_since": AFTER,
                                             "blockers_since": None, "observed_blocked_seconds": 42,
                                             "observed_unavailable_seconds": 20}}],
                    "history": {"event_limit": 16, "byte_limit": 16384, "total_events": 20,
                                "retained_events": 2, "retained_bytes": 1000, "dropped_events": 16,
                                "envelope_omitted_events": 2, "truncated": True,
                                "events": [{"at": AFTER, "check": "notification_pipeline", "state": "blocked",
                                            "cause_sha256": "a" * 64, "blockers": preview},
                                           {"at": AFTER, "check": "notification_pipeline",
                                            "state": "passed" if code == 0 else "unavailable"}]},
                }
                self.env.update(SSH_CODE=str(code), RESPONSE=json.dumps(response))
                process, report = self.invoke()
                self.assertEqual(process.returncode, code)
                self.assertEqual(report["qualification_evidence"], response["qualification_evidence"])
                self.assertFalse(report["had_confirmed_faults"])


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
