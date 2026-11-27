#!/usr/bin/env python3
"""Offline transport and workflow contract tests. No real SSH or notifications."""

import datetime as dt
import copy
import json
import os
import re
import shlex
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

import post_deployment_diagnostics as diagnostic_selection
import post_deployment_builds as builds
import post_deployment_build_history_codec as history_codec
import base64
import zlib
import post_deployment_process as process_evidence
import post_deployment_loops as loop_evidence
import post_deployment_builder as builder_evidence
import post_deployment_handoff as handoff_evidence
import post_deployment_preparation as preparation_evidence
import post_deployment_snapshot_prime as snapshot_prime_evidence
import post_deployment_post_capture as post_capture_evidence
import post_deployment_build_recovery as build_recovery_evidence
import post_deployment_app_task as app_task_evidence
import post_deployment_command_cron as command_cron_evidence
import post_deployment_app_task_operations as operation_evidence
import post_deployment_app_task_results as result_evidence
import post_deployment_command_cron_outcomes as cron_outcome_evidence
import post_deployment_http_cron_delivery as http_cron_delivery_evidence
import post_deployment_delayed_task_dispatch as delayed_task_dispatch_evidence
import post_deployment_command_cron_timing as cron_timing_evidence
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
                self.assertNotIn("--expected-role", args[-1])
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

    def build_evidence(self, role="single-box-v1", state="matching"):
        specs = [("apid", "apid", False), ("gatewayd", "gatewayd-public", False), ("schedd", "schedd", False)]
        single_box = role in ("single-box-v1", "single-box-v2")
        specs += ([("vmmd", "vmmd", False)] if single_box else
                  [("gatewayd-internal", "gatewayd-internal", True), ("schedd-compute", "schedd", True), ("vmmd", "vmmd", True)])
        if role in ("single-box-v2", "control-plane-v2"):
            specs += [("builderd", "builderd", not single_box), ("imaged", "imaged", not single_box)]
        now = dt.datetime.now(dt.UTC).isoformat().replace("+00:00", "Z")
        items = [{"job": job, "daemon": daemon, "node": "operator-only-node", "state": "matching",
                  "target_id": f"{i + 1:064x}", "git_sha": RELEASE, "version": "v9.8.7-internal",
                  "observed_at": now, "scrape_observed_at": now} for i, (job, daemon, _) in enumerate(specs)]
        counts = {"expected": len(specs), "observed": len(specs), "matching": len(specs), "mismatched": 0,
                  "missing": 0, "stale": 0, "unavailable": 0, "unexpected": 0, "distinct_revisions": 1}
        reason = ""
        if state in ("mixed", "mismatched"):
            changed = items[:1] if state == "mixed" else items
            for item in changed:
                item.update(state="mismatched", reason="requested_revision_mismatch", git_sha="b" * 40)
            counts.update(matching=len(items)-len(changed), mismatched=len(changed), distinct_revisions=2 if state == "mixed" else 1)
            reason = "multiple_observed_revisions" if state == "mixed" else "requested_revision_mismatch"
        elif state == "incomplete":
            items[0] = {"job": "apid", "daemon": "apid", "state": "missing", "reason": "expected_target_missing"}
            counts.update(observed=len(items)-1, matching=len(items)-1, missing=1)
            reason = "incomplete_identity_evidence"
        evidence = {
            "generated_at": now, "scope": "selected_critical_daemon_builds", "expected_role": role,
            "requested_release": RELEASE, "evidence_after": AFTER, "state": state,
            "max_age_seconds": 120, "counts": counts,
            "jobs": [{"job": job, "daemon": daemon, "inventory_source": "active_configured_compute_registry" if discovery else "scrape_targets_minimum",
                      "state": "available", "expected": 1} for job, daemon, discovery in specs],
            "items": items, "omitted_items": 0,
            "limitations": ["advisory_not_deployment_qualification", "selected_critical_jobs_not_complete_fleet",
                            "explicit_role_not_deployment_intent", "static_targets_are_scrape_inventory_with_minimum_one",
                            "compute_membership_from_active_configured_registry", "sampled_metrics_not_binary_attestation",
                            "target_addresses_and_raw_labels_excluded", "counts_cover_full_inspected_set_preview_bounded"],
        }
        out = {"advisory": True, "state": state, "evidence": evidence}
        if reason:
            evidence["reason"], out["reason"] = reason, reason
        return out

    def build_response_file(self, response, role="single-box-v1", code=0):
        path = self.root / "build-response.json"
        path.write_text(json.dumps(response, separators=(",", ":")))
        self.env.update(MONITORING_EXPECTED_ROLE=role, SSH_CODE=str(code), RESPONSE_FILE=str(path))
        return path

    def test_runtime_diagnostics_transport_and_canonical_arguments(self):
        for role in ("single-box-v2", "control-plane-v2"):
            for raw in ("none", "http_cron_delivery", "builder_health", "http_cron_delivery,builder_health"):
                for source in ("server", "client"):
                    with self.subTest(role=role, raw=raw, source=source):
                        response, current = self.response(), self.build_evidence(role)
                        self.add_builder_health(current)
                        self.add_http_cron_delivery_evidence(current, "retry")
                        d = diagnostic_selection.parse(role, raw)
                        current.update(diagnostics=d, diagnostics_source=source)
                        response["runtime_builds"] = current
                        self.env["MONITORING_RUNTIME_DIAGNOSTICS"] = raw
                        self.build_response_file(response, role)
                        result, saved = self.invoke()
                        self.assertEqual(result.returncode, 0, result.stderr)
                        expected = copy.deepcopy(current)
                        for key in diagnostic_selection.GROUPS:
                            if not diagnostic_selection.selected(expected, key):
                                expected.pop(key, None)
                        self.assertEqual(saved["runtime_builds"], expected)
                        self.assertEqual(saved["checks"], response["checks"])
                        remote = json.loads((self.root/"capture.json").read_text())["args"][-1]
                        args = shlex.split(remote.splitlines()[1])
                        self.assertEqual(args.count("--runtime-diagnostics"), 1)
                        self.assertEqual(args[args.index("--runtime-diagnostics")+1], diagnostic_selection.query_value(d))
                        summary = (self.root/"summary").read_text()
                        self.assertIn("Not selected:", summary)
                        if "builder_health" not in d["selected"]:
                            self.assertNotIn("Builder evidence unavailable", summary)
                        self.assertNotIn("operator-only-node", summary)

    def test_runtime_diagnostics_legacy_profile_is_core_only(self):
        for role in ("single-box-v1", "control-plane-v1"):
            for raw in ("all", "none"):
                with self.subTest(role=role, raw=raw):
                    response, current = self.response(), self.build_evidence(role)
                    selected = diagnostic_selection.parse(role, raw)
                    self.assertEqual(selected["selected"], [])
                    self.assertEqual(selected["not_selected"], [])
                    current.update(diagnostics=selected, diagnostics_source="server")
                    response["runtime_builds"] = current
                    self.env["MONITORING_RUNTIME_DIAGNOSTICS"] = raw
                    self.build_response_file(response, role)
                    result, saved = self.invoke()
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertEqual(saved["runtime_builds"], current)
                    self.assertEqual(saved["checks"], response["checks"])

    def test_runtime_diagnostics_invalid_request_is_rejected_before_ssh(self):
        for raw, role in (("unknown", "single-box-v2"), ("all,none", "single-box-v2"), ("builder_health,builder_health", "single-box-v2"), ("http_cron_delivery;private-selector", "single-box-v2"), ("http_cron_delivery", "single-box-v1"), ("builder_health", "single-box-v1"), ("none", "")):
            with self.subTest(raw=raw, role=role):
                self.env.update(MONITORING_EXPECTED_ROLE=role, MONITORING_RUNTIME_DIAGNOSTICS=raw)
                result, saved = self.invoke()
                self.assertEqual(result.returncode, 3, result.stderr)
                self.assertEqual(saved["reason"], "invalid_runtime_diagnostics")
                self.assertFalse((self.root/"capture.json").exists())
                self.assertNotIn("private-selector", json.dumps(saved))

    def test_runtime_diagnostics_default_cannot_be_narrowed_by_report(self):
        response, current = self.response(), self.build_evidence("single-box-v2")
        self.add_http_cron_delivery_evidence(current)
        current.update(diagnostics=diagnostic_selection.parse("single-box-v2", "none"), diagnostics_source="server")
        response["runtime_builds"] = current
        self.build_response_file(response, "single-box-v2")
        result, saved = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(saved["runtime_builds"]["http_cron_delivery"], current["http_cron_delivery"])
        self.assertNotIn("diagnostics", saved["runtime_builds"])
        self.assertEqual(saved["runtime_builds"]["diagnostics_unavailable_reason"], "unexpected_diagnostic_selection")
        self.assertEqual(saved["checks"], response["checks"])

    def test_runtime_diagnostics_bad_acknowledgement_keeps_core_and_intent(self):
        for fault in ("scope", "duplicate", "partition", "unknown", "null", "source", "absent"):
            with self.subTest(fault=fault):
                response, current = self.response(), self.build_evidence("single-box-v2")
                self.add_http_cron_delivery_evidence(current)
                wanted = diagnostic_selection.parse("single-box-v2", "http_cron_delivery")
                current.update(diagnostics=copy.deepcopy(wanted), diagnostics_source="server")
                if fault == "scope": current["diagnostics"]["scope"] = "private-selector"
                elif fault == "duplicate": current["diagnostics"]["selected"] *= 2
                elif fault == "partition": current["diagnostics"]["not_selected"] = []
                elif fault == "unknown": current["diagnostics"]["selected"] = ["private-selector"]
                elif fault == "null": current["diagnostics"]["selected"] = None
                elif fault == "source": current["diagnostics_source"] = "private-selector"
                else: current.pop("diagnostics"); current.pop("diagnostics_source")
                response["runtime_builds"] = current
                self.env["MONITORING_RUNTIME_DIAGNOSTICS"] = "http_cron_delivery"
                self.build_response_file(response, "single-box-v2")
                result, saved = self.invoke()
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(saved["checks"], response["checks"])
                out = saved["runtime_builds"]
                self.assertEqual(out["evidence"], current["evidence"])
                self.assertEqual(out["http_cron_delivery"], current["http_cron_delivery"])
                self.assertEqual(out["diagnostics"], wanted)
                self.assertEqual(out["diagnostics_source"], "client")
                self.assertNotIn("private-selector", json.dumps(saved))

    def test_runtime_diagnostics_compact_history_retains_inline_intent(self):
        response, event = self.combined_history_response()
        current = response["runtime_builds"]
        selection = diagnostic_selection.parse("single-box-v2", "none")
        event.update(diagnostics=selection, diagnostics_source="server")
        encoded = history_codec.compact(event, builds.HISTORY_BYTES_MAX)
        p = current["progress"]
        times = {k: builds.timestamp(p[k]) for k in ("first_observed_at", "last_observed_at")}
        out = builds.project_build_event(encoded, "single-box-v2", RELEASE, AFTER, times, p["observations"])
        expanded = history_codec.expand(out)
        self.assertEqual(expanded["diagnostics"], selection)
        self.assertEqual(expanded["diagnostics_source"], "server")
        self.assertIn("loop_health", expanded)
        for name in diagnostic_selection.GROUPS:
            self.assertNotIn(name, expanded)
        self.assertFalse(expanded.get("omitted_sections"))

    def test_runtime_diagnostics_history_matches_requested_view(self):
        response = self.history_response(states=("matching",), role="single-box-v2")
        current = response["runtime_builds"]
        wanted = diagnostic_selection.parse("single-box-v2", "none")
        current.update(diagnostics=wanted, diagnostics_source="server")
        current["history"]["events"][0].update(diagnostics=diagnostic_selection.parse("single-box-v2", "builder_health"), diagnostics_source="server")
        current["history"]["retained_bytes"] = builds.build_history_bytes(current["history"]["events"])
        self.env["MONITORING_RUNTIME_DIAGNOSTICS"] = "none"
        self.build_response_file(response, "single-box-v2")
        result, saved = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(saved["checks"], response["checks"])
        self.assertEqual(saved["runtime_builds"]["evidence"], current["evidence"])
        self.assertEqual(saved["runtime_builds"]["diagnostics"], wanted)
        self.assertEqual(saved["runtime_builds"]["history_unavailable_reason"], "invalid_history")

    def test_runtime_diagnostics_failure_preserves_requested_view(self):
        self.env.update(MONITORING_EXPECTED_ROLE="single-box-v2", MONITORING_RUNTIME_DIAGNOSTICS="none", SSH_CODE="64")
        result, saved = self.invoke()
        self.assertEqual(result.returncode, 3, result.stderr)
        self.assertEqual(saved["reason"], "active_release_changed")
        self.assertEqual(saved["runtime_builds"]["diagnostics"], diagnostic_selection.parse("single-box-v2", "none"))
        self.assertEqual(saved["runtime_builds"]["diagnostics_source"], "client")

    def test_optional_roles_preserve_health_exit_states_and_protected_transport(self):
        for role in ("single-box-v1", "control-plane-v1", "single-box-v2", "control-plane-v2"):
            for state, code in (("passed", 0), ("degraded", 1), ("unknown", 3)):
                with self.subTest(role=role, state=state):
                    response = self.response(state)
                    response["runtime_builds"] = self.build_evidence(role, "mixed")
                    self.build_response_file(response, role, code)
                    process, report = self.invoke()
                    self.assertEqual(process.returncode, code)
                    self.assertEqual(report["checks"], response["checks"])
                    self.assertEqual(report["had_confirmed_faults"], response["had_confirmed_faults"])
                    self.assertEqual(report["runtime_builds"], response["runtime_builds"])
                    capture = json.loads((self.root / "capture.json").read_text())
                    remote = capture["args"][-1]
                    args = shlex.split(remote.splitlines()[1])
                    self.assertEqual(args[-2:], ["--expected-role", role])
                    self.assertEqual(args.count("--expected-role"), 1)
                    self.assertIn("--token-stdin", args)
                    self.assertEqual(capture["stdin"], "fixture-private-token\n")
                    self.assertFalse(capture["secret_in_env"])
                    self.assertNotIn("fixture-private-token", remote)
                    self.assertEqual(remote.count("readlink -f /opt/faas/current"), 2)
                    summary = (self.root / "summary").read_text()
                    self.assertIn("Runtime builds (advisory): **mixed**", summary)
                    self.assertIn("mismatched=1", summary)
                    self.assertNotIn("operator-only-node", summary)
                    self.assertNotIn("v9.8.7-internal", summary)
                    self.assertNotIn("b" * 40, summary)

    def test_matching_mismatched_and_incomplete_builds_are_advisory(self):
        for state in ("matching", "mismatched", "incomplete"):
            with self.subTest(state=state):
                response = self.response()
                response["runtime_builds"] = self.build_evidence(state=state)
                self.build_response_file(response)
                process, report = self.invoke()
                self.assertEqual(process.returncode, 0)
                self.assertFalse(report["had_confirmed_faults"])
                self.assertEqual(report["runtime_builds"], response["runtime_builds"])
                self.assertIn("**" + state + "**", (self.root / "summary").read_text())

    def test_invalid_role_never_contacts_ssh_or_echoes_input(self):
        for role in ("control-plane", " single-box-v1", "single-box-v1; private-provider-error", "single-box-v1\n--admin-token=fixture-private-token"):
            with self.subTest(role=role):
                self.env["MONITORING_EXPECTED_ROLE"] = role
                process, report = self.invoke()
                self.assertEqual(process.returncode, 3)
                self.assertEqual(report["reason"], "invalid_expected_role")
                self.assertEqual(report["runtime_builds"]["reason"], "invalid_expected_role")
                self.assertFalse((self.root / "capture.json").exists())

    def test_initialize_and_transport_failure_keep_explicit_unknown_builds(self):
        self.env["MONITORING_EXPECTED_ROLE"] = "control-plane-v1"
        process, report = self.invoke("--initialize")
        self.assertEqual(process.returncode, 0)
        self.assertEqual(report["runtime_builds"], {"advisory": True, "state": "unknown", "reason": "verification_not_started"})
        self.assertFalse((self.root / "capture.json").exists())
        self.env["SSH_CODE"] = "2"  # An older CLI cannot accept the new option.
        process, report = self.invoke()
        self.assertEqual(process.returncode, 3)
        self.assertEqual(report["reason"], "verification_unavailable")
        self.assertEqual(report["runtime_builds"]["state"], "unknown")
        self.assertEqual(report["runtime_builds"]["reason"], "verification_unavailable")

    def test_missing_section_and_older_api_preserve_healthy_qualification(self):
        response = self.response()
        self.build_response_file(response)
        process, report = self.invoke()
        self.assertEqual(process.returncode, 0)
        self.assertEqual(report["runtime_builds"]["reason"], "evidence_not_returned")
        response["runtime_builds"] = {"advisory": True, "state": "unknown", "reason": "source_endpoint_not_found", "source_http_status": 404}
        self.build_response_file(response)
        process, report = self.invoke()
        self.assertEqual(process.returncode, 0)
        self.assertEqual(report["runtime_builds"], response["runtime_builds"])
        self.assertIn("source HTTP 404", (self.root / "summary").read_text())

    def test_malformed_advisory_data_cannot_leak_or_change_healthy_qualification(self):
        valid = self.build_evidence()
        samples = ["private-provider-error", {"advisory": False, "state": "matching"},
                   {"advisory": True, "state": "unknown", "reason": "private-provider-error"}]
        changes = [
            (("evidence", "requested_release"), "b" * 40),
            (("evidence", "expected_role"), "control-plane-v1"),
            (("evidence", "evidence_after"), "2000-01-01T00:00:00Z"),
            (("evidence", "generated_at"), (dt.datetime.now(dt.UTC)+dt.timedelta(hours=1)).isoformat()),
            (("evidence", "counts", "matching"), True),
            (("evidence", "counts", "observed"), -1),
            (("evidence", "items", 0, "node"), "https://private-provider-error"),
            (("evidence", "items", 0, "version"), "v" * 129),
            (("evidence", "items", 0, "observed_at"), (dt.datetime.now(dt.UTC)-dt.timedelta(minutes=3)).isoformat()),
            (("evidence", "jobs", 0, "inventory_source"), "private-provider-error"),
            (("evidence", "omitted_items"), 1),
            (("envelope_omitted_items",), False),
            (("source_http_status",), 1000),
            (("source_http_status",), 503),
            (("evidence", "items"), [copy.deepcopy(valid["evidence"]["items"][0]) for _ in range(33)]),
        ]
        for path, value in changes:
            sample = copy.deepcopy(valid)
            target = sample
            for part in path[:-1]:
                target = target[part]
            target[path[-1]] = value
            samples.append(sample)
        for i, sample in enumerate(samples):
            with self.subTest(sample=i):
                response = self.response()
                response["runtime_builds"] = sample
                self.build_response_file(response)
                process, report = self.invoke()
                self.assertEqual(process.returncode, 0)
                self.assertEqual(report["checks"], response["checks"])
                self.assertFalse(report["had_confirmed_faults"])
                self.assertEqual(report["runtime_builds"], {"advisory": True, "state": "unknown", "reason": "invalid_advisory_evidence"})

    def test_unknown_optional_properties_are_removed_before_artifact_and_summary(self):
        response = self.response()
        value = self.build_evidence()
        for target in (value, value["evidence"], value["evidence"]["counts"], value["evidence"]["jobs"][0], value["evidence"]["items"][0]):
            target["provider_debug"] = {"token": "fixture-private-token", "error": "private-provider-error"}
        response["runtime_builds"] = value
        self.build_response_file(response)
        process, report = self.invoke()
        self.assertEqual(process.returncode, 0)
        self.assertEqual(report["runtime_builds"]["state"], "matching")
        self.assertNotIn("provider_debug", self.report.read_text())

    def test_full_set_counts_and_source_availability_survive_truncated_preview(self):
        response = self.response()
        value = self.build_evidence("control-plane-v1", "mixed")
        evidence = value["evidence"]
        evidence["jobs"][3].update(state="unknown", reason="inventory_unavailable", expected=0)
        evidence["counts"].update(expected=5, observed=1001, matching=1000, mismatched=1)
        evidence["items"] = []
        evidence["omitted_items"] = 1001
        value["envelope_omitted_items"] = 32
        response["runtime_builds"] = value
        self.build_response_file(response, "control-plane-v1")
        process, report = self.invoke()
        self.assertEqual(process.returncode, 0)
        self.assertEqual(report["runtime_builds"], value)
        summary = (self.root / "summary").read_text()
        self.assertIn("matching=1000, mismatched=1", summary)
        self.assertIn("gatewayd-internal: unknown (inventory_unavailable)", summary)
        self.assertIn("omitted_items=1001; envelope_omitted_items=32", summary)

    def test_oversized_optional_section_is_unknown_without_losing_core_result(self):
        response = self.response()
        value = self.build_evidence()
        value["evidence"]["provider_debug"] = "private-provider-error" * 4000
        response["runtime_builds"] = value
        self.build_response_file(response)
        process, report = self.invoke()
        self.assertEqual(process.returncode, 0)
        self.assertEqual(report["runtime_builds"]["reason"], "invalid_advisory_evidence")

    def test_advisory_preview_yields_to_complete_artifact_limit(self):
        response = self.response()
        value = self.build_evidence()
        evidence = value["evidence"]
        while len(evidence["items"]) < 32:
            item = copy.deepcopy(evidence["items"][0])
            item["target_id"] = f"{len(evidence['items']) + 1:064x}"
            evidence["items"].append(item)
        evidence["counts"].update(matching=32, observed=32)
        response["runtime_builds"] = value
        response["padding"] = "x" * ((1 << 20)-len(json.dumps(response, indent=2).encode())-1+1000)
        source = self.build_response_file(response)
        original = source.read_bytes()
        process, report = self.invoke()
        self.assertEqual(process.returncode, 0)
        self.assertLessEqual(self.report.stat().st_size, 1 << 20)
        result = report["runtime_builds"]
        self.assertEqual(result["evidence"]["counts"], evidence["counts"])
        self.assertGreater(result["envelope_omitted_items"], 0)
        self.assertEqual(result["evidence"]["omitted_items"] + len(result["evidence"]["items"]), 32)
        self.assertEqual(source.read_bytes(), original)

    def test_build_transport_bounds_follow_the_existing_go_policy(self):
        source = (ROOT / "pkg/api/limits.go").read_text()
        for name, value in (("ObsRuntimeBuildPreviewMax", builds.PREVIEW_MAX), ("ObsRuntimeBuildNodesMax", builds.NODES_MAX),
                            ("ObsRuntimeBuildSamplesMax", builds.SAMPLES_MAX), ("ObsRuntimeBuildVersionMaxBytes", builds.VERSION_MAX_BYTES),
                            ("ObsServingProbeNodeMaxBytes", builds.NODE_MAX_BYTES), ("ObsRuntimeBuildJobsMax", builds.JOBS_MAX)):
            self.assertEqual(int(re.search(r"\b"+name+r"\s*=\s*(\d+)", source)[1]), value)
        self.assertRegex(source, r"ObsRuntimeBuildResponseMaxBytes\s*=\s*64\s*<<\s*10")
        self.assertEqual(builds.BUILD_MAX_BYTES, 64 << 10)
        self.assertEqual(process_evidence.LOOKBACK_SECONDS, 600)
        self.assertEqual(process_evidence.UPTIME_MAX_SECONDS, 10*365*24*3600)
        self.assertRegex(source, r"ObsRuntimeProcessLookback\s*=\s*10\s*\*\s*time.Minute")
        self.assertRegex(source, r"ObsRuntimeProcessUptimeMax\s*=\s*10\s*\*\s*365\s*\*\s*24\s*\*\s*time.Hour")
        self.assertRegex(source, r"ObsRuntimeProcessStartTolerance\s*=\s*ObsHealthClockSkewTolerance\s*\+\s*time.Second")
        self.assertRegex(source, r"ObsRuntimeProcessResetTolerance\s*=\s*time.Second")
        self.assertEqual(process_evidence.START_TOLERANCE_SECONDS, builds.CLOCK_SKEW_SECONDS+1)
        self.assertEqual(process_evidence.RESET_TOLERANCE_SECONDS, 1)
        self.assertRegex(source, r"ObsRuntimeReadinessReasonsMax\s*=\s*10\b")
        self.assertRegex(source, r"ObsRuntimeReadinessReasonSamplesMax\s*=\s*ObsRuntimeBuildSamplesMax\s*\*\s*ObsRuntimeReadinessReasonsMax")
        self.assertRegex(source, r"ObsPostDeployBuildEventsMax\s*=\s*"+str(builds.HISTORY_EVENTS_MAX)+r"\b")
        self.assertRegex(source, r"ObsPostDeployBuildHistoryBytesMax\s*=\s*16\s*<<\s*10")
        self.assertEqual(builds.HISTORY_BYTES_MAX, 16 << 10)
        for name, value in (("ObsInputHealthMaxAge", builds.MAX_AGE_SECONDS), ("ObsHealthClockSkewTolerance", builds.CLOCK_SKEW_SECONDS)):
            self.assertRegex(source, name+r"\s*=\s*"+str(value)+r"\s*\*\s*time.Second")

    def test_advisory_metadata_can_yield_without_changing_health(self):
        response = self.response()
        value = self.build_evidence()
        value["evidence"].update(items=[], omitted_items=4)
        value["envelope_omitted_items"] = 4
        response["runtime_builds"] = value
        response["padding"] = "x" * ((1 << 20)-len(json.dumps(response, indent=2).encode())-1+100)
        source = self.build_response_file(response)
        self.assertLess(source.stat().st_size + 1, 1 << 20)
        process, report = self.invoke()
        self.assertEqual(process.returncode, 0)
        self.assertFalse(report["had_confirmed_faults"])
        self.assertLessEqual(self.report.stat().st_size, 1 << 20)
        self.assertEqual(report["runtime_builds"], {"advisory": True, "state": "unknown", "reason": "report_envelope"})

    def test_mandatory_report_cannot_exceed_the_serialized_artifact_limit(self):
        response = self.response()
        response["oversized_mandatory_field"] = [0] * 150000
        self.build_response_file(response)
        process, report = self.invoke()
        self.assertEqual(process.returncode, 3)
        self.assertEqual(report["reason"], "invalid_verification_report")
        self.assertLessEqual(self.report.stat().st_size, 1 << 20)

    def test_unselected_new_cli_diagnostic_preserves_default_transport(self):
        response = self.response()
        response["runtime_builds"] = {"advisory": True, "state": "unknown", "reason": "expected_role_not_selected"}
        self.build_response_file(response, "")
        process, report = self.invoke()
        self.assertEqual(process.returncode, 0)
        self.assertEqual(report["runtime_builds"], response["runtime_builds"])
        remote = json.loads((self.root / "capture.json").read_text())["args"][-1]
        self.assertNotIn("--expected-role", remote)

    def combined_history_response(self, role="single-box-v2"):
        response = self.history_response(states=("matching",), role=role)
        current = response["runtime_builds"]
        for name in ("add_readiness", "add_loop_health", "add_builder_health", "add_handoff_health", "add_preparation_health", "add_snapshot_prime_health", "add_post_capture_health", "add_build_recovery_health", "add_app_task_health", "add_command_cron_health", "add_operation_evidence", "add_result_evidence", "add_cron_outcome_evidence", "add_cron_timing_evidence", "add_http_cron_delivery_evidence", "add_delayed_task_dispatch_evidence"):
            getattr(self, name)(current)
        event = current["history"]["events"][0]
        e = current["evidence"]
        event.update(readiness=copy.deepcopy(e["readiness"]), process=copy.deepcopy(e["process"]))
        for section in history_codec.SECTIONS:
            event[section] = {k: copy.deepcopy(v) for k, v in current[section].items() if k not in ("items", "comparison_at", "omitted_items")}
        return response, event

    def test_compact_combined_history_profiles_survive_transport(self):
        for role in ("single-box-v2", "control-plane-v2"):
            with self.subTest(role=role):
                response, event = self.combined_history_response(role)
                self.assertGreater(builds.build_history_bytes([event]), builds.HISTORY_BYTES_MAX)
                encoded = history_codec.compact(event, builds.HISTORY_BYTES_MAX)
                self.assertIn("compact_details", encoded)
                self.assertEqual(history_codec.expand(encoded), event)
                h = response["runtime_builds"]["history"]
                h.update(events=[encoded], retained_bytes=builds.build_history_bytes([encoded]))
                self.build_response_file(response, role)
                result, saved = self.invoke()
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(saved["checks"], response["checks"])
                stored = saved["runtime_builds"]["history"]
                self.assertEqual(stored["retained_events"], 1)
                self.assertLessEqual(stored["retained_bytes"], builds.HISTORY_BYTES_MAX)
                self.assertEqual(history_codec.expand(stored["events"][0]), event)
                self.assertEqual(saved["runtime_builds"]["progress"], response["runtime_builds"]["progress"])
                summary = (self.root/"summary").read_text()
                self.assertIn("Losslessly compacted history events=1", summary)
                self.assertNotIn(encoded["compact_details"]["data"], summary)
                self.assertNotIn("operator-only-node", summary)

    def test_compact_history_projection_removes_private_fields(self):
        response, event = self.combined_history_response()
        event["app_task_health"]["private_error"] = "private-provider-error"
        encoded = history_codec.encode(event)
        h = response["runtime_builds"]["history"]
        h.update(events=[encoded], retained_bytes=builds.build_history_bytes([encoded]))
        self.build_response_file(response, "single-box-v2")
        result, saved = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        stored = saved["runtime_builds"]["history"]["events"][0]
        self.assertNotEqual(stored["compact_details"]["data"], encoded["compact_details"]["data"])
        self.assertNotIn("private-provider-error", json.dumps(history_codec.expand(stored)))
        self.assertNotIn("private_error", json.dumps(history_codec.expand(stored)))

    def test_compact_history_projection_size_fitting_preserves_newest(self):
        _, event = self.combined_history_response("control-plane-v2")
        events = []
        for i in range(8):
            value = copy.deepcopy(event)
            value["observation"] = i+1
            events.append(history_codec.encode(value))
        self.assertGreater(builds.build_history_bytes(events), builds.HISTORY_BYTES_MAX)
        fitted, omitted = history_codec.fit_projected_events(events, builds.HISTORY_BYTES_MAX)
        self.assertEqual(len(fitted)+omitted, len(events))
        self.assertGreater(len(fitted), 0)
        self.assertEqual(fitted[-1]["observation"], 8)
        self.assertLessEqual(builds.build_history_bytes(fitted), builds.HISTORY_BYTES_MAX)
        self.assertEqual(len(events), 8)
        # A larger encoding of one validated event must yield to compaction or
        # explicit detail omissions, never lose that newest event altogether.
        core = {k: v for k, v in event.items() if k not in history_codec.SECTIONS}
        core["omitted_sections"] = list(history_codec.SECTIONS)
        fitted, omitted = history_codec.fit_projected_events([events[-1]], builds.build_history_bytes([core]))
        self.assertEqual(len(fitted), 1)
        self.assertEqual(omitted, 0)
        self.assertIn("omitted_sections", fitted[0])
        self.assertEqual(fitted[0]["observation"], 8)

    def test_malformed_compact_history_is_isolated(self):
        for fault in ("encoding", "data", "length", "ceiling", "negative", "boolean", "base64", "newline", "truncated", "trailing", "concatenated", "unknown_section", "duplicate_key", "null_section", "duplicate_inline", "omitted_and_encoded", "bomb"):
            with self.subTest(fault=fault):
                response, event = self.combined_history_response()
                encoded = history_codec.encode(event)
                p = encoded["compact_details"]
                def pack(body, trailing=b""):
                    p.update(decoded_bytes=len(body), data=base64.b64encode(zlib.compress(body)+trailing).decode())
                if fault == "encoding":
                    p["encoding"] = "unknown"
                elif fault == "data":
                    p.pop("data")
                elif fault == "length":
                    p["decoded_bytes"] -= 1
                elif fault == "ceiling":
                    p["decoded_bytes"] = history_codec.DECODED_MAX+1
                elif fault == "negative":
                    p["decoded_bytes"] = -1
                elif fault == "boolean":
                    p["decoded_bytes"] = True
                elif fault == "base64":
                    p["data"] = "!"
                elif fault == "newline":
                    p["data"] += "\n"
                elif fault == "truncated":
                    p["data"] = p["data"][:-4]
                elif fault in ("trailing", "concatenated"):
                    pack(b'{"app_task_health":{}}', b"extra" if fault == "trailing" else zlib.compress(b"{}"))
                elif fault == "unknown_section":
                    pack(b'{"private_error":{"message":"private-provider-error"}}')
                elif fault == "duplicate_key":
                    pack(b'{"app_task_health":{"state":"empty","state":"nonempty"}}')
                elif fault == "null_section":
                    pack(b'{"app_task_health":null}')
                elif fault == "duplicate_inline":
                    encoded["app_task_health"] = event["app_task_health"]
                elif fault == "omitted_and_encoded":
                    encoded["omitted_sections"] = ["app_task_health"]
                elif fault == "bomb":
                    pack(b"x"*(history_codec.DECODED_MAX+1))
                    p["decoded_bytes"] = 1
                h = response["runtime_builds"]["history"]
                h.update(events=[encoded], retained_bytes=builds.build_history_bytes([encoded]))
                self.build_response_file(response, "single-box-v2")
                result, saved = self.invoke()
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(saved["checks"], response["checks"])
                self.assertEqual(saved["runtime_builds"]["history_unavailable_reason"], "invalid_history")
                self.assertEqual(saved["runtime_builds"]["app_task_health"], response["runtime_builds"]["app_task_health"])

    def test_explicit_history_detail_omissions_and_envelope_accounting(self):
        with self.assertRaises(ValueError):
            history_codec.expand({"state": "unknown", "omitted_sections": ["loop_health"]})
        response, event = self.combined_history_response()
        core = {k: v for k, v in event.items() if k not in history_codec.SECTIONS}
        core["omitted_sections"] = list(history_codec.SECTIONS)
        fallback = history_codec.compact(event, builds.build_history_bytes([core]))
        self.assertEqual(fallback, core)
        h = response["runtime_builds"]["history"]
        h.update(events=[fallback], retained_bytes=builds.build_history_bytes([fallback]), detail_omitted_events=1, truncated=True)
        self.build_response_file(response, "single-box-v2")
        result, saved = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(saved["runtime_builds"]["history"], h)
        self.assertIn("History detail omitted for:", (self.root/"summary").read_text())
        # Detail loss cannot substitute for missing first events, or be counted
        # as an event dropped by the recorder/report envelope.
        for fault in ("missing_counter", "wrong_counter", "unknown_section", "duplicate_section", "inline_conflict", "forged_start"):
            sample = copy.deepcopy(response)
            h = sample["runtime_builds"]["history"]
            if fault == "missing_counter":
                h.pop("detail_omitted_events")
            elif fault == "wrong_counter":
                h["detail_omitted_events"] = 0
            elif fault == "unknown_section":
                h["events"][0]["omitted_sections"] = ["arbitrary"]
            elif fault == "duplicate_section":
                h["events"][0]["omitted_sections"].append("app_task_health")
            elif fault == "inline_conflict":
                h["events"][0]["app_task_health"] = event["app_task_health"]
            else:
                h["events"][0]["observation"] = 2
            h["retained_bytes"] = builds.build_history_bytes(h["events"])
            self.build_response_file(sample, "single-box-v2")
            _, saved = self.invoke()
            self.assertEqual(saved["runtime_builds"]["history_unavailable_reason"], "invalid_history", fault)
        # Envelope fitting recomputes retained detail omissions independently.
        report = copy.deepcopy(response)
        for name in history_codec.SECTIONS:
            section = report["runtime_builds"][name]
            section["omitted_items"] += len(section["items"])
            section["items"] = []
        e = report["runtime_builds"]["evidence"]
        e["omitted_items"] += len(e["items"])
        e["items"] = []
        maximum = len(json.dumps(report, indent=2).encode())-200
        builds.fit_runtime_builds(report, maximum)
        h = report["runtime_builds"]["history"]
        self.assertEqual(h["detail_omitted_events"], 0)
        self.assertEqual(h["envelope_omitted_events"], 1)
        self.assertEqual(h["dropped_events"], 0)

    def history_response(self, states=("mixed", "unknown", "matching"), ages=None, after=AFTER, role="single-box-v1"):
        now = dt.datetime.now(dt.UTC)
        ages = ages or list(range(len(states), 0, -1))
        at = [(now-dt.timedelta(seconds=age)).isoformat().replace("+00:00", "Z") for age in ages]
        events = []
        previous = None
        for i, state in enumerate(states):
            if state == "unknown":
                current = {"advisory": True, "state": "unknown", "reason": "source_server_error", "source_http_status": 503}
                event = {"at": at[i], "observation": i+1, "state": "unknown", "reason": "source_server_error", "source_http_status": 503}
            else:
                current = self.build_evidence(role=role, state=state)
                current["evidence"].update(generated_at=at[i], evidence_after=after)
                for item in current["evidence"]["items"]:
                    if item.get("target_id"):
                        item.update(observed_at=at[i], scrape_observed_at=at[i])
                event = {"at": at[i], "observation": i+1, "state": current["state"], "evidence_generated_at": at[i],
                         "counts": copy.deepcopy(current["evidence"]["counts"]), "jobs": copy.deepcopy(current["evidence"]["jobs"])}
                if current.get("reason"):
                    event["reason"] = current["reason"]
            if state != previous:
                events.append(event)
            previous = state
        progress = {"observations": len(states), "regressions": 0,
                    "matching_throughout_observed_polls": all(state == "matching" for state in states),
                    "observed_incomplete_evidence": any(state in ("unknown", "incomplete") for state in states),
                    "observed_source_unavailable": "unknown" in states,
                    "first_observed_at": at[0], "last_observed_at": at[-1]}
        for kinds, first_key, last_key in ((("mixed", "mismatched"), "first_mismatch_at", "last_mismatch_at"), (("matching",), "first_matching_at", "last_matching_at")):
            indexes = [i for i, state in enumerate(states) if state in kinds]
            if indexes:
                progress[first_key], progress[last_key] = at[indexes[0]], at[indexes[-1]]
        pending, matching = False, False
        for i, state in enumerate(states):
            if state in ("mixed", "mismatched"):
                if matching:
                    progress["regressions"] += 1
                    progress["last_regression_at"] = at[i]
                pending, matching = True, False
            elif state == "matching":
                if pending:
                    progress["last_recovery_at"] = at[i]
                pending, matching = False, True
        total, dropped = len(events), 0
        while len(events) > 16 or builds.build_history_bytes(events) > 16 << 10:
            events.pop(0)
            dropped += 1
        current["progress"] = progress
        current["history"] = {"scope": "observed_build_state_and_coverage_counts", "event_limit": 16,
                              "byte_limit": 16 << 10, "total_events": total, "retained_events": len(events),
                              "retained_bytes": builds.build_history_bytes(events), "dropped_events": dropped,
                              "envelope_omitted_events": 0, "truncated": dropped > 0, "events": events}
        response = self.response()
        response.update(attempts=len(states), evidence_after=after, started_at=(now-dt.timedelta(seconds=ages[0]+1)).isoformat().replace("+00:00", "Z"),
                        updated_at=at[-1], runtime_builds=current)
        return response

    def add_readiness(self, current, state="ready", reason="ready"):
        e = current["evidence"]
        counts = dict.fromkeys(("ready", "not_ready", "missing", "stale", "conflicting", "unavailable"), 0)
        counts["ready"] = len(e["items"])
        if state != "ready":
            counts["ready"] -= 1
            counts[state] += 1
        r = {"state": "not_ready" if state == "not_ready" else "ready" if state == "ready" else "incomplete",
             "counts": counts, "reasons": [{"reason": "ready", "count": counts["ready"]}]}
        if state == "not_ready":
            r.update(reason="fresh_not_ready")
            r["reasons"].append({"reason": reason, "count": 1})
        elif state != "ready":
            r["reason"] = "incomplete_readiness_evidence"
        e["readiness"] = r
        for item in e["items"]:
            item["readiness"] = {"state": "ready", "reason": "ready", "observed_at": item["observed_at"],
                                 "reason_observed_at": item["observed_at"]}
        if state != "ready":
            e["items"][0]["readiness"].update(state=state, reason=reason)

    def test_readiness_states_survive_transport_without_changing_health(self):
        for role in builds.ROLES:
            for state, reason in (("ready", "ready"), ("not_ready", "draining"), ("not_ready", "database"),
                                  ("missing", "readiness_missing"), ("stale", "readiness_stale"),
                                  ("conflicting", "conflicting_readiness"), ("unavailable", "source_unavailable")):
                with self.subTest(role=role, state=state, reason=reason):
                    response = self.response()
                    response["runtime_builds"] = self.build_evidence(role)
                    self.add_readiness(response["runtime_builds"], state, reason)
                    self.build_response_file(response, role)
                    process, report = self.invoke()
                    self.assertEqual(process.returncode, 0)
                    self.assertEqual(report["checks"], response["checks"])
                    self.assertEqual(report["runtime_builds"], response["runtime_builds"])
                    summary = (self.root / "summary").read_text()
                    self.assertIn("Runtime readiness (advisory)", summary)
                    self.assertIn(state + "=", summary)
                    self.assertNotIn("operator-only-node", summary)

    def test_invalid_optional_readiness_preserves_build_identity_and_health(self):
        for fault in ("raw_reason", "bad_count", "stale_ready", "missing_scrape", "future_reason", "unknown_field"):
            response = self.response()
            current = self.build_evidence()
            self.add_readiness(current)
            r = current["evidence"]["readiness"]
            item = current["evidence"]["items"][0]
            if fault == "raw_reason":
                r["reasons"][0]["reason"] = "private-provider-error"
            elif fault == "bad_count":
                r["counts"]["ready"] += 1
            elif fault == "stale_ready":
                item["readiness"]["observed_at"] = (dt.datetime.now(dt.UTC)-dt.timedelta(minutes=5)).isoformat()
            elif fault == "missing_scrape":
                # A valid missing build item cannot carry proven readiness.
                item.clear()
                item.update(job="apid", daemon="apid", state="missing", reason="expected_target_missing",
                            readiness={"state": "ready", "reason": "ready"})
                current["state"], current["reason"] = "incomplete", "incomplete_identity_evidence"
                current["evidence"].update(state="incomplete", reason="incomplete_identity_evidence")
                current["evidence"]["counts"].update(matching=3, observed=3, missing=1)
            elif fault == "future_reason":
                item["readiness"]["reason_observed_at"] = (dt.datetime.now(dt.UTC)+dt.timedelta(minutes=5)).isoformat()
            else:
                r["raw_error"] = item["readiness"]["raw_error"] = "private-provider-error"
            response["runtime_builds"] = current
            self.build_response_file(response)
            process, report = self.invoke()
            self.assertEqual(process.returncode, 0)
            saved = report["runtime_builds"]["evidence"]
            self.assertEqual(saved["counts"], current["evidence"]["counts"])
            if fault == "unknown_field":
                self.assertIn("readiness", saved)
            else:
                self.assertEqual(saved["readiness_unavailable_reason"], "invalid_readiness")
                self.assertTrue(all("readiness" not in item for item in saved["items"]))

    def test_readiness_loss_and_recovery_history_survives_collection(self):
        response = self.history_response(states=("matching", "matching", "matching"))
        current = response["runtime_builds"]
        first, last = current["progress"]["first_observed_at"], current["progress"]["last_observed_at"]
        middle = (dt.datetime.fromisoformat(first.replace("Z", "+00:00"))+dt.timedelta(seconds=1)).isoformat().replace("+00:00", "Z")
        self.add_readiness(current)
        events = []
        for i, (at, state, reason) in enumerate(((first, "ready", "ready"), (middle, "not_ready", "draining"), (last, "ready", "ready"))):
            sample = self.build_evidence()
            self.add_readiness(sample, state, reason)
            events.append({"at": at, "observation": i+1, "state": "matching", "evidence_generated_at": at,
                           "counts": sample["evidence"]["counts"], "jobs": sample["evidence"]["jobs"],
                           "readiness": sample["evidence"]["readiness"]})
        current["progress"]["readiness"] = {"losses": 1, "ready_throughout_observed_polls": False,
                                                "observed_incomplete_evidence": False, "first_ready_at": first,
                                                "last_ready_at": last, "first_not_ready_at": middle,
                                                "last_not_ready_at": middle, "last_loss_at": middle,
                                                "last_recovery_at": last}
        current["history"].update(events=events, total_events=3, retained_events=3, retained_bytes=builds.build_history_bytes(events))
        response["attempts"] = 3
        self.build_response_file(response)
        process, report = self.invoke()
        self.assertEqual(process.returncode, 0)
        self.assertEqual(report["runtime_builds"]["history"], current["history"])
        self.assertEqual(report["runtime_builds"]["progress"], current["progress"])
        self.assertIn("losses=1", (self.root / "summary").read_text())
        # Invalid readiness progress is isolated from valid build history.
        current["progress"]["readiness"]["losses"] = 0
        self.build_response_file(response)
        _, report = self.invoke()
        self.assertEqual(report["runtime_builds"]["progress"]["readiness_unavailable_reason"], "invalid_history")
        self.assertEqual(report["runtime_builds"]["history"], current["history"])

    def test_readiness_not_ready_with_unknown_inventory_keeps_gap(self):
        import post_deployment_readiness as readiness
        response = self.history_response(states=("matching",))
        current = response["runtime_builds"]
        self.add_readiness(current, "not_ready", "draining")
        current["evidence"]["jobs"][0].update(state="unknown", reason="inventory_unavailable")
        at = current["progress"]["last_observed_at"]
        clocks = {name: dt.datetime.fromisoformat(current["progress"][name].replace("Z", "+00:00"))
                  for name in ("first_observed_at", "last_observed_at")}
        progress = {"losses": 0, "ready_throughout_observed_polls": False, "observed_incomplete_evidence": True,
                    "first_not_ready_at": at, "last_not_ready_at": at}
        self.assertEqual(readiness.project_progress(progress, current["progress"], clocks, current, []), progress)
        progress["observed_incomplete_evidence"] = False
        with self.assertRaises(ValueError):
            readiness.project_progress(progress, current["progress"], clocks, current, [])

    def add_process(self, current, state="baseline", comparison_at=None, relation="same"):
        e = current["evidence"]
        now = dt.datetime.fromisoformat(e["generated_at"].replace("Z", "+00:00"))
        previous = comparison_at or (now-dt.timedelta(seconds=30)).isoformat().replace("+00:00", "Z")
        counts = dict.fromkeys(process_evidence.COUNTS, 0)
        counts["fresh"] = len(e["items"])
        raw = {"state": "baseline", "reason": "awaiting_comparison", "incomplete": False, "counts": counts}
        for item in e["items"]:
            item["process"] = {"state": "fresh", "uptime_seconds": 130, "observed_at": e["generated_at"], "comparison": "baseline"}
        if state != "baseline":
            raw.update(state="unchanged", comparison_at=previous)
            raw.pop("reason")
            counts.update(compared=len(e["items"]), unchanged=len(e["items"]))
            for item in e["items"]:
                item["process"].update(comparison="unchanged", previous_uptime_seconds=100, previous_observed_at=previous)
            first = e["items"][0]["process"]
            if state == "changed":
                first.update(uptime_seconds=5, comparison="changed", revision_relation=relation)
                counts.update(changed=1, unchanged=counts["unchanged"]-1)
                counts[relation+"_revision_changes"] = 1
                raw.update(state="changed", reason="observed_process_change")
            elif state in ("missing", "stale", "conflicting", "unavailable"):
                first.clear()
                first.update(state=state, reason={"missing": "uptime_missing", "stale": "uptime_stale", "conflicting": "conflicting_uptime", "unavailable": "source_unavailable"}[state])
                counts[state], counts["fresh"] = 1, counts["fresh"]-1
                counts["compared"] -= 1
                counts["unchanged"] -= 1
                raw.update(state="incomplete", reason="incomplete_continuity_evidence", incomplete=True)
            elif state in ("new_target", "repeated_sample"):
                first["comparison"] = state
                key = "new_targets" if state == "new_target" else "repeated_samples"
                counts[key] = 1
                counts["compared"] -= 1
                counts["unchanged"] -= 1
                if state == "new_target":
                    first.pop("previous_uptime_seconds")
                    first.pop("previous_observed_at")
                    counts["removed_targets"] = 1
                else:
                    first.update(uptime_seconds=100, observed_at=previous)
                raw.update(state="incomplete", reason="incomplete_continuity_evidence", incomplete=True)
        e["process"] = raw

    def test_process_evidence_profiles_and_states_preserve_health(self):
        for role in builds.ROLES:
            for state in ("baseline", "unchanged", "changed", "missing", "stale", "conflicting", "unavailable", "new_target", "repeated_sample"):
                with self.subTest(role=role, state=state):
                    response = self.response()
                    response["runtime_builds"] = self.build_evidence(role)
                    self.add_process(response["runtime_builds"], state)
                    self.build_response_file(response, role)
                    result, report = self.invoke()
                    self.assertEqual(result.returncode, 0)
                    self.assertEqual(report["checks"], response["checks"])
                    self.assertEqual(report["runtime_builds"], response["runtime_builds"])
                    summary = (self.root / "summary").read_text()
                    self.assertIn("Runtime process continuity (advisory)", summary)
                    self.assertNotIn("operator-only-node", summary)
                    self.assertNotIn("v9.8.7-internal", summary)

    def test_bad_optional_process_data_preserves_identity_and_readiness(self):
        for fault in ("false_change", "invalid_count", "negative_uptime", "future_sample", "clock_regression", "raw_error", "extra_field"):
            response = self.response()
            current = self.build_evidence()
            self.add_readiness(current)
            self.add_process(current, "changed")
            e, sample = current["evidence"], current["evidence"]["items"][0]["process"]
            if fault == "false_change":
                sample["uptime_seconds"] = 130
            elif fault == "invalid_count":
                e["process"]["counts"]["changed"] += 1
            elif fault == "negative_uptime":
                sample["uptime_seconds"] = -1
            elif fault == "future_sample":
                sample["observed_at"] = (dt.datetime.now(dt.UTC)+dt.timedelta(minutes=5)).isoformat()
            elif fault == "clock_regression":
                sample["observed_at"] = sample["previous_observed_at"]
            elif fault == "raw_error":
                sample["comparison_reason"] = "private-provider-error"
            else:
                sample["raw_error"] = e["process"]["raw_error"] = "private-provider-error"
            response["runtime_builds"] = current
            self.build_response_file(response)
            result, report = self.invoke()
            self.assertEqual(result.returncode, 0)
            saved = report["runtime_builds"]["evidence"]
            self.assertEqual(saved["counts"], e["counts"])
            self.assertEqual(saved["readiness"], e["readiness"])
            if fault == "extra_field":
                self.assertIn("process", saved)
            else:
                self.assertEqual(saved["process_unavailable_reason"], "invalid_process")
                self.assertTrue(all("process" not in item for item in saved["items"]))

    def test_process_history_source_gaps_and_summary(self):
        response = self.history_response(states=("matching", "matching", "matching", "unknown", "matching"), ages=(90, 70, 50, 30, 10))
        current = response["runtime_builds"]
        start = dt.datetime.fromisoformat(current["progress"]["first_observed_at"].replace("Z", "+00:00"))
        clocks = [(start+dt.timedelta(seconds=i*20)).isoformat().replace("+00:00", "Z") for i in range(5)]
        events = []
        for i, state in enumerate(("baseline", "unchanged", "changed", "unknown", "unchanged")):
            if state == "unknown":
                events.append({"at": clocks[i], "observation": i+1, "state": "unknown", "reason": "source_server_error", "source_http_status": 503})
                continue
            value = self.build_evidence()
            value["evidence"]["generated_at"] = clocks[i]
            for item in value["evidence"]["items"]:
                item.update(observed_at=clocks[i], scrape_observed_at=clocks[i])
            self.add_process(value, state, comparison_at=clocks[2] if i == 4 else clocks[max(i-1, 0)])
            events.append({"at": clocks[i], "observation": i+1, "state": "matching", "evidence_generated_at": clocks[i],
                           "counts": value["evidence"]["counts"], "jobs": value["evidence"]["jobs"], "process": value["evidence"]["process"]})
            if i == 4:
                current["evidence"]["process"] = value["evidence"]["process"]
                for target, sample in zip(current["evidence"]["items"], value["evidence"]["items"]):
                    target["process"] = sample["process"]
        progress = dict.fromkeys(process_evidence.PROGRESS, 0)
        progress.update(polls_with_comparison=3, compared_pairs=12, observed_changes=1, same_revision_changes=1,
                        first_change_at=clocks[2], last_change_at=clocks[2], observed_incomplete_evidence=True)
        current["progress"]["process"] = progress
        current["history"].update(events=events, total_events=5, retained_events=5, retained_bytes=builds.build_history_bytes(events))
        self.build_response_file(response)
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0)
        self.assertEqual(report["runtime_builds"]["history"], current["history"])
        self.assertEqual(report["runtime_builds"]["progress"], current["progress"])
        self.assertIn("observed_changes=1", (self.root / "summary").read_text())
        progress["observed_changes"] = 99
        self.build_response_file(response)
        _, report = self.invoke()
        self.assertEqual(report["runtime_builds"]["progress"]["process_unavailable_reason"], "invalid_history")
        self.assertEqual(report["runtime_builds"]["history"], current["history"])

    def test_process_projection_preserves_nanosecond_comparison_clocks(self):
        self.assertEqual(process_evidence.compare(130, "2026-11-15T12:00:30.000000001Z", 100,
                                                  "2026-11-15T12:00:00.000000001Z"), ("unchanged", ""))
        self.assertEqual(process_evidence.compare(5, "2026-11-15T12:00:30.000000002Z", 100,
                                                  "2026-11-15T12:00:30.000000001Z"), ("changed", ""))
        self.assertEqual(process_evidence.compare(130, "2026-11-15T12:00:30.000000001Z", 100,
                                                  "2026-11-15T12:00:30.000000002Z"), ("unavailable", "sample_clock_regressed"))

    def test_process_header_keeps_nanosecond_comparison_boundary(self):
        current = self.build_evidence()
        self.add_process(current, "changed")
        base = dt.datetime.now(dt.UTC).strftime("%Y-%m-%dT%H:%M:%S")
        e = current["evidence"]
        e["generated_at"] = base+".000000002Z"
        e["process"]["comparison_at"] = base+".000000001Z"
        for item in e["items"]:
            item["process"]["observed_at"] = e["generated_at"]
        saved = builds.project_runtime_builds(current, "single-box-v1", RELEASE, AFTER)
        self.assertEqual(saved["evidence"]["process"]["comparison_at"], base+".000000001Z")

    def add_loop_health(self, current, state="no_reported_stall"):
        e = current["evidence"]
        counts = dict.fromkeys(loop_evidence.COUNTS, 0)
        items = []
        by_job = {item["job"]: item for item in e["items"]}
        for job in e["jobs"]:
            for loop in loop_evidence.expected_loops(job["daemon"]):
                parent = by_job[job["job"]]
                items.append({"job": job["job"], "daemon": job["daemon"], "loop": loop,
                              "node": parent.get("node", ""), "target_id": parent["target_id"],
                              "state": "no_reported_stall", "reported_stalled": False, "age_seconds": 1,
                              "scrape_observed_at": e["generated_at"], "stall_observed_at": e["generated_at"],
                              "age_observed_at": e["generated_at"]})
                counts["expected"] += job["expected"]
                counts["observed"] += 1
                counts["no_reported_stall"] += 1
        raw = {"state": "no_reported_stall", "counts": counts, "incomplete": False, "items": items, "omitted_items": 0}
        first = next(item for item in items if item["loop"] == "main")
        if state != "no_reported_stall":
            counts["no_reported_stall"] -= 1
            first.update(state="stalled", reported_stalled=True, age_seconds=240)
            counts["stalled"], counts["reported_stalls"] = 1, 1
            raw.update(state="stalled", reason="reported_loop_stall")
            if state in ("missing_age", "invalid_age"):
                first.update(state="missing" if state == "missing_age" else "unavailable",
                             reason="loop_age_missing" if state == "missing_age" else "invalid_loop_age")
                first.pop("age_seconds")
                first.pop("age_observed_at")
                counts["stalled"] = 0
                counts[first["state"]] = 1
                raw["incomplete"] = True
            elif state == "stale_stall":
                first.update(state="stale", reason="loop_sample_stale", stall_observed_at=(dt.datetime.now(dt.UTC)-dt.timedelta(minutes=5)).isoformat())
                first.pop("reported_stalled")
                counts.update(stalled=0, reported_stalls=0, stale=1)
                raw.update(state="incomplete", reason="incomplete_loop_evidence", incomplete=True)
        current["loop_health"] = raw

    def test_loop_health_profiles_and_partial_reports_preserve_health(self):
        for role in builds.ROLES:
            for state in ("no_reported_stall", "stalled", "missing_age", "invalid_age", "stale_stall"):
                with self.subTest(role=role, state=state):
                    response = self.response()
                    response["runtime_builds"] = self.build_evidence(role)
                    self.add_loop_health(response["runtime_builds"], state)
                    self.build_response_file(response, role)
                    result, report = self.invoke()
                    self.assertEqual(result.returncode, 0)
                    self.assertEqual(report["checks"], response["checks"])
                    self.assertEqual(report["runtime_builds"], response["runtime_builds"])
                    summary = (self.root / "summary").read_text()
                    self.assertIn("Reported work-loop health (advisory)", summary)
                    self.assertNotIn("operator-only-node", summary)
                    if state in ("stalled", "missing_age", "invalid_age"):
                        self.assertIn("reported_stalls=1", summary)

    def test_invalid_loop_metadata_keeps_other_evidence(self):
        for fault in ("unknown_loop", "bad_type", "stale_report", "negative_age", "incomplete_flag", "too_few_loops", "unknown_field"):
            response = self.response()
            current = self.build_evidence()
            self.add_readiness(current)
            self.add_process(current, "unchanged")
            self.add_loop_health(current)
            loops, first = current["loop_health"], current["loop_health"]["items"][0]
            if fault == "unknown_loop":
                first["loop"] = "private-provider-error"
            elif fault == "bad_type":
                first["reported_stalled"] = "private-provider-error"
            elif fault == "stale_report":
                first["stall_observed_at"] = (dt.datetime.now(dt.UTC)-dt.timedelta(minutes=5)).isoformat()
            elif fault == "negative_age":
                first["age_seconds"] = -1
            elif fault == "incomplete_flag":
                loops["incomplete"] = True
            elif fault == "too_few_loops":
                loops["items"] = [first]
                loops["counts"].update(observed=1, no_reported_stall=1)
            else:
                loops["raw_error"] = first["raw_error"] = "private-provider-error"
            response["runtime_builds"] = current
            self.build_response_file(response)
            result, report = self.invoke()
            self.assertEqual(result.returncode, 0)
            saved = report["runtime_builds"]
            self.assertEqual(saved["evidence"], current["evidence"])
            if fault == "unknown_field":
                self.assertIn("loop_health", saved)
            else:
                self.assertEqual(saved["loop_health_unavailable_reason"], "invalid_loop_evidence")
                self.assertNotIn("loop_health", saved)

    def test_loop_stall_recovery_history_survives_collection(self):
        response = self.history_response(states=("matching", "matching", "unknown", "matching"), ages=(4, 3, 2, 1))
        current = response["runtime_builds"]
        times = [(dt.datetime.fromisoformat(current["progress"]["first_observed_at"].replace("Z", "+00:00"))+dt.timedelta(seconds=i)).isoformat().replace("+00:00", "Z") for i in range(4)]
        events = []
        for i, state in enumerate(("no_reported_stall", "stalled", "unknown", "no_reported_stall")):
            if state == "unknown":
                events.append({"at": times[i], "observation": i+1, "state": "unknown", "reason": "source_server_error", "source_http_status": 503})
                continue
            value = self.build_evidence()
            value["evidence"]["generated_at"] = times[i]
            self.add_loop_health(value, state)
            summary = {key: value["loop_health"][key] for key in ("state", "counts", "incomplete", "reason") if key in value["loop_health"]}
            events.append({"at": times[i], "observation": i+1, "state": "matching", "evidence_generated_at": times[i],
                           "counts": value["evidence"]["counts"], "jobs": value["evidence"]["jobs"], "loop_health": summary})
            if i == 3:
                current["loop_health"] = value["loop_health"]
        p = {"stall_regressions": 1, "no_reported_stall_throughout_observed_polls": False,
             "observed_incomplete_evidence": True, "first_stall_at": times[1], "last_stall_at": times[1],
             "first_no_reported_stall_at": times[0], "last_no_reported_stall_at": times[3],
             "last_recovery_at": times[3], "last_regression_at": times[1]}
        current["progress"]["loop_health"] = p
        current["history"].update(events=events, total_events=4, retained_events=4, retained_bytes=builds.build_history_bytes(events))
        self.build_response_file(response)
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0)
        self.assertEqual(report["runtime_builds"]["history"], current["history"])
        self.assertEqual(report["runtime_builds"]["progress"], current["progress"])
        self.assertIn("stall_regressions=1", (self.root / "summary").read_text())
        p["stall_regressions"] = 0
        self.build_response_file(response)
        _, report = self.invoke()
        self.assertEqual(report["runtime_builds"]["progress"]["loop_health_unavailable_reason"], "invalid_history")
        self.assertEqual(report["runtime_builds"]["history"], current["history"])

    def test_loop_detail_yields_to_the_report_envelope(self):
        response = self.response()
        current = self.build_evidence()
        self.add_loop_health(current, "stalled")
        response["runtime_builds"] = current
        response["padding"] = "x"*((1 << 20)-len(json.dumps(response, indent=2).encode())-1+300)
        self.build_response_file(response)
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0)
        self.assertEqual(report["runtime_builds"]["loop_health"]["counts"], current["loop_health"]["counts"])
        self.assertGreater(report["runtime_builds"]["loop_envelope_omitted_items"], 0)
        self.assertLessEqual(self.report.stat().st_size, 1 << 20)

    def add_builder_health(self, current, state="waiting"):
        e = current["evidence"]
        target = next(item for item in e["items"] if item["job"] == "builderd")
        now = dt.datetime.fromisoformat(e["generated_at"].replace("Z", "+00:00"))
        clock = lambda seconds: (now-dt.timedelta(seconds=seconds)).isoformat().replace("+00:00", "Z")
        worker_state = "overdue" if state == "partial" else "waiting" if state == "pressure" else state
        values = {"enabled": worker_state != "draining", "inflight": worker_state == "working",
                  "started_at": clock(3600), "last_attempt_at": clock(5), "last_completed_at": clock(4),
                  "interval_seconds": 2, "busy_allowance_seconds": 900,
                  "progress_age_seconds": 4, "progress_allowance_seconds": 64}
        if worker_state == "working":
            values.update(last_attempt_at=clock(600), last_completed_at=clock(660), progress_age_seconds=600, progress_allowance_seconds=964)
        elif worker_state == "overdue":
            values.update(last_attempt_at=clock(91), last_completed_at=clock(90), progress_age_seconds=90)
        counts = dict.fromkeys(builder_evidence.COUNTS, 0)
        counts.update(expected=1, observed=1, complete=1)
        counts[worker_state+"_workers"] = 1
        component = lambda: {"state": "available", "observed_at": e["generated_at"]}
        item = {"node": target["node"], "target_id": target["target_id"], "scrape_observed_at": e["generated_at"],
                "state": "available", "worker": {"state": worker_state, "observed_at": e["generated_at"], "values": values},
                "slots": {**component(), "values": {"used": 0, "limit": 1}},
                "queues": [{**component(), "queue": queue, "values": {"enabled": True, "waiting": 0, "active": 0, "capacity": 64, "workers": 2, "oldest_wait_seconds": 0}} for queue in ("build", "cancel")]}
        h = {"state": "overdue" if worker_state == "overdue" else "observed", "counts": counts, "incomplete": False, "items": [item], "omitted_items": 0}
        if worker_state == "overdue":
            h["reason"] = "worker_progress_overdue"
        if state == "partial":
            h["incomplete"] = True
            counts.update(complete=0, missing=1)
            item["state"], item["slots"] = "missing", {"state": "missing", "reason": "metric_missing"}
        elif state == "pressure":
            item["slots"]["values"]["used"] = 1
            item["queues"][0]["values"].update(waiting=1, oldest_enqueued_at=clock(120), oldest_wait_seconds=120)
            counts.update(saturated_slots=1, waiting_queues=1)
        current["builder_health"] = h

    def test_builder_progress_and_pressure_survive_transport(self):
        for role in ("single-box-v2", "control-plane-v2"):
            for state in ("waiting", "working", "overdue", "draining", "partial", "pressure"):
                with self.subTest(role=role, state=state):
                    response = self.response()
                    current = self.build_evidence(role)
                    self.add_readiness(current)
                    self.add_loop_health(current)
                    self.add_builder_health(current, state)
                    response["runtime_builds"] = current
                    self.build_response_file(response, role)
                    result, report = self.invoke()
                    self.assertEqual(result.returncode, 0)
                    self.assertEqual(report["checks"], response["checks"])
                    self.assertEqual(report["runtime_builds"], current)
                    summary = (self.root/"summary").read_text()
                    self.assertIn("Builder progress and pressure (advisory)", summary)
                    self.assertNotIn("operator-only-node", summary)
                    if state in ("overdue", "partial"):
                        self.assertIn("overdue_workers=1", summary)

    def test_invalid_builder_evidence_preserves_other_sections(self):
        for fault in ("missing_flag", "null_count", "queue_name", "stale", "age", "impossible_queue", "incomplete", "unknown_field", "v1"):
            response = self.response()
            current = self.build_evidence("single-box-v2")
            self.add_readiness(current)
            self.add_loop_health(current)
            self.add_builder_health(current, "draining" if fault == "missing_flag" else "waiting")
            h = current["builder_health"]
            item = h["items"][0]
            if fault == "missing_flag":
                item["worker"]["values"].pop("enabled")
            elif fault == "null_count":
                item["slots"]["values"]["used"] = None
            elif fault == "queue_name":
                item["queues"][0]["queue"] = "private-provider-error"
            elif fault == "stale":
                item["worker"]["observed_at"] = (dt.datetime.now(dt.UTC)-dt.timedelta(minutes=4)).isoformat()
            elif fault == "age":
                item["worker"]["values"]["progress_age_seconds"] = 0
            elif fault == "impossible_queue":
                item["queues"][0]["values"]["active"] = 3
            elif fault == "incomplete":
                h["incomplete"] = True
            elif fault == "unknown_field":
                h["raw_error"] = item["worker"]["values"]["raw_error"] = "private-provider-error"
            else:
                old = self.build_evidence()
                old["builder_health"] = h
                current = old
            response["runtime_builds"] = current
            self.build_response_file(response, "single-box-v1" if fault == "v1" else "single-box-v2")
            result, report = self.invoke()
            self.assertEqual(result.returncode, 0)
            saved = report["runtime_builds"]
            self.assertEqual(saved["evidence"], current["evidence"])
            if fault == "unknown_field":
                self.assertIn("builder_health", saved)
            else:
                self.assertNotIn("builder_health", saved)
                self.assertEqual(saved["builder_health_unavailable_reason"], "invalid_builder_evidence")
            self.assertNotIn("private-provider-error", json.dumps(report))

    def test_builder_history_and_recovery_survive_collection(self):
        response = self.history_response(states=("matching", "matching", "unknown", "matching"), ages=(4, 3, 2, 1), role="single-box-v2")
        current = response["runtime_builds"]
        first = dt.datetime.fromisoformat(current["progress"]["first_observed_at"].replace("Z", "+00:00"))
        clocks = [(first+dt.timedelta(seconds=i)).isoformat().replace("+00:00", "Z") for i in range(4)]
        events = []
        for i, state in enumerate(("waiting", "overdue", "gap", "working")):
            if state == "gap":
                events.append({"at": clocks[i], "observation": i+1, "state": "unknown", "reason": "source_server_error", "source_http_status": 503})
                continue
            value = self.build_evidence("single-box-v2")
            value["evidence"]["generated_at"] = clocks[i]
            self.add_builder_health(value, state)
            h = value["builder_health"]
            summary = {key: h[key] for key in ("state", "reason", "counts", "incomplete") if key in h}
            events.append({"at": clocks[i], "observation": i+1, "state": "matching", "evidence_generated_at": clocks[i], "counts": value["evidence"]["counts"], "jobs": value["evidence"]["jobs"], "builder_health": summary})
            if i == 3:
                current["builder_health"] = h
        p = {"overdue_regressions": 1, "within_allowance_throughout_observed_polls": False, "observed_incomplete_evidence": True,
             "first_overdue_at": clocks[1], "last_overdue_at": clocks[1], "first_within_allowance_at": clocks[0], "last_within_allowance_at": clocks[3], "last_recovery_at": clocks[3], "last_regression_at": clocks[1]}
        current["progress"]["builder_health"] = p
        current["history"].update(events=events, total_events=4, retained_events=4, retained_bytes=builds.build_history_bytes(events))
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0)
        self.assertEqual(report["runtime_builds"]["history"], current["history"])
        self.assertEqual(report["runtime_builds"]["progress"], current["progress"])
        self.assertIn("overdue_regressions=1", (self.root/"summary").read_text())
        p["overdue_regressions"] = 0
        self.build_response_file(response, "single-box-v2")
        _, report = self.invoke()
        self.assertEqual(report["runtime_builds"]["progress"]["builder_health_unavailable_reason"], "invalid_history")
        self.assertEqual(report["runtime_builds"]["history"], current["history"])

    def test_builder_detail_yields_before_other_report_evidence(self):
        response = self.response()
        current = self.build_evidence("single-box-v2")
        self.add_builder_health(current, "overdue")
        response["runtime_builds"] = current
        response["padding"] = "x"*((1 << 20)-len(json.dumps(response, indent=2).encode())-1+300)
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0)
        self.assertEqual(report["runtime_builds"]["builder_health"]["counts"], current["builder_health"]["counts"])
        self.assertEqual(report["runtime_builds"]["evidence"], current["evidence"])
        self.assertEqual(report["runtime_builds"]["builder_envelope_omitted_items"], 1)
        self.assertLessEqual(self.report.stat().st_size, 1 << 20)

    def test_builder_projection_accepts_nanosecond_generation_clock(self):
        current = self.build_evidence("single-box-v2")
        base = dt.datetime.now(dt.UTC).replace(microsecond=0)
        current["evidence"]["generated_at"] = base.strftime("%Y-%m-%dT%H:%M:%S")+".000000001Z"
        self.add_builder_health(current)
        worker = current["builder_health"]["items"][0]["worker"]["values"]
        worker["progress_age_seconds"] = 4.000000001
        parsed = builder_evidence.project(current["builder_health"], current["evidence"])
        self.assertEqual(parsed["items"][0]["worker"]["values"]["progress_age_seconds"], 4.000000001)

    def add_build_recovery_health(self, current, state="empty"):
        e = current["evidence"]
        target = next(i for i in e["items"] if i["job"] == "apid")
        now = dt.datetime.fromisoformat(e["generated_at"].replace("Z", "+00:00"))
        counts = dict.fromkeys(build_recovery_evidence.COUNTS, 0)
        counts.update(expected=1, observed=1, complete=1, empty_views=1)
        component = lambda v: {"state": "empty", "observed_at": e["generated_at"], "values": v}
        item = {"job": "apid", "node": target["node"], "target_id": target["target_id"], "scrape_observed_at": e["generated_at"], "state": "available",
                "collection": {"state": "available", "observed_at": e["generated_at"], "values": {"success": True, "last_success_at": e["generated_at"]}},
                "running": component({"count": 0}), "cleanup": component({"pending": 0, "retried": 0})}
        h = {"scope": build_recovery_evidence.SCOPE, "state": "empty", "counts": counts, "incomplete": False, "items": [item], "omitted_items": 0}
        if state in ("running", "cleanup", "both", "partial_running", "partial_cleanup"):
            h.update(state="nonempty", reason="build_recovery_evidence_observed")
            counts.update(nonempty_views=1, empty_views=0)
            if state in ("running", "both", "partial_running"):
                counts["running_views"] = 1
                item["running"].update(state="nonempty", values={"count": 2,
                    "oldest_started_at": (now-dt.timedelta(hours=3)).isoformat().replace("+00:00", "Z"), "oldest_age_seconds": 10800})
            if state in ("cleanup", "both", "partial_cleanup"):
                counts.update(cleanup_views=1, retried_cleanup_views=1)
                item["cleanup"].update(state="nonempty", values={"pending": 100000, "retried": 3,
                    "oldest_created_at": (now-dt.timedelta(hours=24)).isoformat().replace("+00:00", "Z"), "oldest_age_seconds": 86400})
            if state.startswith("partial_"):
                h["incomplete"], item["state"] = True, "missing"
                counts.update(complete=0, missing=1)
                item["cleanup" if state == "partial_running" else "running"] = {"state": "missing", "reason": "metric_missing"}
        elif state in ("collection_failed", "never_observed", "stale_collection"):
            h.update(state="incomplete", reason="incomplete_build_recovery_evidence", incomplete=True)
            item["state"] = "stale" if state == "stale_collection" else "unavailable"
            counts.update(complete=0, empty_views=0)
            counts[item["state"]] = 1
            collection = item["collection"]["values"]
            reason = "collection_stale" if state == "stale_collection" else "collection_failed"
            if state == "stale_collection":
                collection["last_success_at"] = (now-dt.timedelta(seconds=180)).isoformat().replace("+00:00", "Z")
            else:
                collection["success"] = False
                counts["unsuccessful_collections"] = 1
                if state == "never_observed":
                    collection.pop("last_success_at")
                    reason = "collection_not_observed"
            for name in ("running", "cleanup"):
                item[name] = {"state": item["state"], "reason": reason}
        current["build_recovery_health"] = h

    def test_build_recovery_components_survive_transport(self):
        for role in ("single-box-v2", "control-plane-v2"):
            for state in ("empty", "running", "cleanup", "both", "partial_running", "partial_cleanup", "collection_failed", "never_observed", "stale_collection"):
                with self.subTest(role=role, state=state):
                    response, current = self.response(), self.build_evidence(role)
                    self.add_handoff_health(current)
                    self.add_post_capture_health(current)
                    self.add_build_recovery_health(current, state)
                    response["runtime_builds"] = current
                    self.build_response_file(response, role)
                    result, report = self.invoke()
                    self.assertEqual(result.returncode, 0)
                    self.assertEqual(report["checks"], response["checks"])
                    self.assertEqual(report["runtime_builds"], current)
                    summary = (self.root/"summary").read_text()
                    self.assertIn("Build recovery and VM cleanup (advisory)", summary)
                    self.assertNotIn("operator-only-node", summary)
                    self.assertNotIn("pending=100000", summary)
                    self.assertNotIn("enabled", current["build_recovery_health"]["counts"])

    def test_invalid_build_recovery_is_isolated(self):
        for fault in ("missing_scope", "wrong_scope", "wrong_job", "missing_success", "missing_count", "null_pending", "missing_retried", "missing_age", "bad_clock", "different_scrape", "bad_counts", "invalid_subset", "stale_collection", "unknown_field"):
            with self.subTest(fault=fault):
                response, current = self.response(), self.build_evidence("single-box-v2")
                self.add_post_capture_health(current)
                self.add_build_recovery_health(current, "both")
                h = current["build_recovery_health"]
                item = h["items"][0]
                if fault == "missing_scope":
                    h.pop("scope")
                elif fault == "wrong_scope":
                    h["scope"] = "unique_builds"
                elif fault == "wrong_job":
                    item["job"] = "imaged"
                elif fault == "missing_success":
                    item["collection"]["values"].pop("success")
                elif fault == "missing_count":
                    item["running"]["values"].pop("count")
                elif fault == "null_pending":
                    item["cleanup"]["values"]["pending"] = None
                elif fault == "missing_retried":
                    item["cleanup"]["values"].pop("retried")
                elif fault == "missing_age":
                    item["cleanup"]["values"].pop("oldest_age_seconds")
                elif fault == "bad_clock":
                    item["cleanup"]["values"]["oldest_created_at"] = "bad-clock"
                elif fault == "different_scrape":
                    item["cleanup"]["observed_at"] = (dt.datetime.fromisoformat(item["cleanup"]["observed_at"].replace("Z", "+00:00"))-dt.timedelta(seconds=1)).isoformat()
                elif fault == "bad_counts":
                    h["counts"]["running_views"] = 0
                elif fault == "invalid_subset":
                    item["cleanup"]["values"]["retried"] = 100001
                elif fault == "stale_collection":
                    item["collection"]["values"]["last_success_at"] = (dt.datetime.now(dt.UTC)-dt.timedelta(minutes=10)).isoformat()
                else:
                    item["private_error"] = "private-provider-error"
                response["runtime_builds"] = current
                self.build_response_file(response, "single-box-v2")
                result, report = self.invoke()
                self.assertEqual(result.returncode, 0)
                saved = report["runtime_builds"]
                self.assertEqual(saved["post_capture_health"], current["post_capture_health"])
                self.assertEqual(saved["evidence"], current["evidence"])
                if fault == "unknown_field":
                    self.assertIn("build_recovery_health", saved)
                    self.assertNotIn("private-provider-error", json.dumps(saved))
                else:
                    self.assertNotIn("build_recovery_health", saved)
                    self.assertEqual(saved["build_recovery_health_unavailable_reason"], "invalid_build_recovery_evidence")

    def test_build_recovery_history_preserves_gaps_and_sampled_empty(self):
        response = self.history_response(states=("matching", "matching", "unknown", "matching"), ages=(4, 3, 2, 1), role="single-box-v2")
        current = response["runtime_builds"]
        first = dt.datetime.fromisoformat(current["progress"]["first_observed_at"].replace("Z", "+00:00"))
        clocks = [(first+dt.timedelta(seconds=i)).isoformat().replace("+00:00", "Z") for i in range(4)]
        events = []
        for i, state in enumerate(("empty", "cleanup", "gap", "empty")):
            if state == "gap":
                events.append({"at": clocks[i], "observation": i+1, "state": "unknown", "reason": "source_server_error", "source_http_status": 503})
                continue
            value = self.build_evidence("single-box-v2")
            value["evidence"]["generated_at"] = clocks[i]
            self.add_build_recovery_health(value, state)
            h = value["build_recovery_health"]
            events.append({"at": clocks[i], "observation": i+1, "state": "matching", "evidence_generated_at": clocks[i], "counts": value["evidence"]["counts"], "jobs": value["evidence"]["jobs"], "build_recovery_health": {k: h[k] for k in ("scope", "state", "reason", "counts", "incomplete") if k in h}})
            if i == 3:
                current["build_recovery_health"] = h
        p = {"nonempty_transitions": 1, "empty_throughout_observed_polls": False, "observed_incomplete_evidence": True,
             "first_nonempty_at": clocks[1], "last_nonempty_at": clocks[1], "first_empty_at": clocks[0], "last_empty_at": clocks[3]}
        current["progress"]["build_recovery_health"] = p
        current["history"].update(events=events, total_events=4, retained_events=4, retained_bytes=builds.build_history_bytes(events))
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0)
        self.assertEqual(report["runtime_builds"]["history"], current["history"])
        self.assertEqual(report["runtime_builds"]["progress"], current["progress"])
        p["waiting_transitions"] = p.pop("nonempty_transitions")
        self.build_response_file(response, "single-box-v2")
        _, report = self.invoke()
        self.assertEqual(report["runtime_builds"]["progress"]["build_recovery_health_unavailable_reason"], "invalid_history")
        self.assertEqual(report["runtime_builds"]["history"], current["history"])

    def test_build_recovery_replica_overlap_and_clipping(self):
        response, current = self.response(), self.build_evidence("single-box-v2")
        self.add_build_recovery_health(current, "cleanup")
        h = current["build_recovery_health"]
        h["items"].append(copy.deepcopy(h["items"][0]))
        h["items"][1]["target_id"] = "f"*64
        for k in ("observed", "complete", "nonempty_views", "cleanup_views", "retried_cleanup_views"):
            h["counts"][k] = 2
        response["runtime_builds"] = current
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0)
        self.assertEqual(report["runtime_builds"]["build_recovery_health"], h)
        self.assertNotIn("pending", h["counts"])
        response["padding"] = "x"*((1 << 20)-len(json.dumps(response, indent=2).encode())-1+300)
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0)
        self.assertEqual(report["runtime_builds"]["build_recovery_health"]["counts"], h["counts"])
        self.assertEqual(report["runtime_builds"]["evidence"], current["evidence"])
        self.assertGreater(report["runtime_builds"]["build_recovery_envelope_omitted_items"], 0)
        self.assertLessEqual(self.report.stat().st_size, 1 << 20)

    def test_build_recovery_retry_preserves_obligation_creation_origin(self):
        current = self.build_evidence("single-box-v2")
        self.add_build_recovery_health(current, "cleanup")
        h = current["build_recovery_health"]
        first = build_recovery_evidence.project(h, current["evidence"])["items"][0]["cleanup"]["values"]
        h["items"][0]["cleanup"]["values"]["retried"] += 1
        later = build_recovery_evidence.project(h, current["evidence"])["items"][0]["cleanup"]["values"]
        self.assertEqual(first["oldest_created_at"], later["oldest_created_at"])
        self.assertEqual(first["oldest_age_seconds"], later["oldest_age_seconds"])
        h["items"][0]["cleanup"]["values"]["pending"] = 0
        with self.assertRaises(ValueError):
            build_recovery_evidence.project(h, current["evidence"])

    def add_app_task_health(self, current, state="empty"):
        e = current["evidence"]
        targets = [item for item in e["items"] if item["job"] in app_task_evidence.jobs_for_role(e["expected_role"])]
        target = targets[0]
        now = dt.datetime.fromisoformat(e["generated_at"].replace("Z", "+00:00"))
        component = lambda v: {"state": "available", "observed_at": e["generated_at"], "values": v}
        counts = dict.fromkeys(app_task_evidence.COUNTS, 0)
        counts.update(expected=1, observed=1, complete=1, empty_views=1, enabled_views=1, dispatch_enabled_views=1, runner_running_views=1)
        classes = [{"kind": p[0], "class": p[1], "nonempty_views": 0, "empty_views": 1, "unknown_age_views": 0} for p in app_task_evidence.CLASSES]
        values = [{"kind": p[0], "class": p[1], "state": "empty", "observed_at": e["generated_at"], "values": {"pending": 0, "unknown_age": 0}} for p in app_task_evidence.CLASSES]
        item = {"node": target["node"], "target_id": target["target_id"], "scrape_observed_at": e["generated_at"], "state": "available",
                "collection": component({"success": True, "last_success_at": e["generated_at"]}),
                "observation": component({"enabled": True}), "classes": values,
                "dispatch": {"state": "running", "observed_at": e["generated_at"], "values": {"enabled": True, "running": True, "slots_configured": 4, "slots_in_use": 0, "last_claim_success_at": e["generated_at"], "last_sweep_success_at": e["generated_at"]}}}
        h = {"scope": app_task_evidence.SCOPE, "state": "empty", "counts": counts, "classes": classes, "incomplete": False, "items": [item], "omitted_items": 0}
        if state in ("nonempty", "unknown_ages", "partial", "disabled"):
            h.update(state="nonempty", reason="app_task_evidence_observed")
            counts.update(nonempty_views=1, empty_views=0)
            classes[0].update(nonempty_views=1, empty_views=0)
            values[0].update(state="nonempty", values={"pending": 100000, "unknown_age": 1,
                             "oldest_boundary_at": (now-dt.timedelta(seconds=3600)).isoformat().replace("+00:00", "Z"), "oldest_known_age_seconds": 3600})
            classes[0]["unknown_age_views"] = counts["unknown_age_views"] = 1
            if state == "unknown_ages":
                values[0]["values"] = {"pending": 100000, "unknown_age": 100000}
        if state == "partial":
            h["incomplete"], item["state"] = True, "missing"
            counts.update(complete=0, missing=1)
            classes[2]["empty_views"] = 0
            values[2].clear()
            values[2].update({"kind": "release", "class": "held", "state": "missing", "reason": "metric_missing"})
        elif state == "disabled":
            item["observation"]["values"]["enabled"] = False
            counts.update(enabled_views=0, disabled_views=1)
        elif state in ("collection_failed", "never_observed", "stale_collection"):
            h.update(state="incomplete", reason="incomplete_app_task_evidence", incomplete=True)
            item["state"] = "stale" if state == "stale_collection" else "unavailable"
            counts.update(complete=0, empty_views=0)
            counts[item["state"]] = 1
            collection = item["collection"]["values"]
            reason = "collection_stale" if state == "stale_collection" else "collection_failed"
            if state == "stale_collection":
                collection["last_success_at"] = (now-dt.timedelta(seconds=180)).isoformat().replace("+00:00", "Z")
            else:
                collection["success"] = False
                counts["unsuccessful_collections"] = 1
                if state == "never_observed":
                    collection.pop("last_success_at")
                    reason = "collection_not_observed"
            for cohort, value in zip(classes, values):
                cohort["empty_views"] = 0
                value.clear()
                value.update({"kind": cohort["kind"], "class": cohort["class"], "state": item["state"], "reason": reason})
        if state in ("dispatch_disabled", "runner_stopped", "saturated"):
            d = item["dispatch"]
            if state == "dispatch_disabled":
                d["state"] = "disabled"
                d["values"].update(enabled=False, running=False)
                counts.update(dispatch_enabled_views=0, runner_running_views=0, dispatch_disabled_views=1)
            elif state == "runner_stopped":
                d["state"] = "stopped"
                d["values"]["running"] = False
                counts.update(runner_running_views=0, runner_stopped_views=1)
            else:
                d["values"]["slots_in_use"] = 4
                counts.update(occupied_views=1, saturated_views=1)
        h["items"] = []
        for target in targets:
            clone = copy.deepcopy(item)
            clone.update(job=target["job"], target_id=target["target_id"], node=target["node"])
            h["items"].append(clone)
        for key in counts:
            counts[key] *= len(targets)
        counts["expected"] = sum(j["expected"] for j in e["jobs"] if j["job"] in app_task_evidence.jobs_for_role(e["expected_role"]))
        for cls in classes:
            for key in app_task_evidence.CLASS_COUNTS:
                cls[key] *= len(targets)
        current["app_task_health"] = h

    def test_app_task_classes_survive_transport(self):
        for role in ("single-box-v2", "control-plane-v2"):
            for state in ("empty", "nonempty", "unknown_ages", "partial", "disabled", "collection_failed", "never_observed", "stale_collection", "dispatch_disabled", "runner_stopped", "saturated"):
                with self.subTest(role=role, state=state):
                    response, current = self.response(), self.build_evidence(role)
                    self.add_handoff_health(current)
                    self.add_app_task_health(current, state)
                    response["runtime_builds"] = current
                    self.build_response_file(response, role)
                    result, report = self.invoke()
                    self.assertEqual(result.returncode, 0)
                    self.assertEqual(report["checks"], response["checks"])
                    self.assertEqual(report["runtime_builds"], current)
                    summary = (self.root/"summary").read_text()
                    self.assertIn("App tasks and release commands (advisory)", summary)
                    self.assertNotIn("operator-only-node", summary)
                    self.assertNotIn("pending=100000", summary)
                    if state == "unknown_ages":
                        self.assertNotIn("oldest_known_age_seconds", report["runtime_builds"]["app_task_health"]["items"][0]["classes"][0]["values"])

    def test_invalid_app_task_is_isolated(self):
        for fault in ("missing_success", "missing_enabled", "missing_unknown", "missing_age", "null_pending", "bad_clock", "snapshot_clocks", "class_order", "bad_counts", "missing_scope", "wrong_scope", "missing_kind", "bad_kind", "missing_running", "missing_slots", "invalid_slots", "unknown_field"):
            response, current = self.response(), self.build_evidence("single-box-v2")
            self.add_handoff_health(current)
            self.add_app_task_health(current, "nonempty")
            h = current["app_task_health"]
            item, cohort = h["items"][0], h["items"][0]["classes"][0]
            if fault == "missing_kind":
                cohort.pop("kind")
            elif fault == "bad_kind":
                cohort["kind"] = "arbitrary"
            elif fault == "missing_running":
                item["dispatch"]["values"].pop("running")
            elif fault == "missing_slots":
                item["dispatch"]["values"].pop("slots_in_use")
            elif fault == "invalid_slots":
                item["dispatch"]["values"]["slots_in_use"] = 5
            elif fault == "missing_scope":
                h.pop("scope")
            elif fault == "wrong_scope":
                h["scope"] = "unique_deployments"
            elif fault == "missing_success":
                del item["collection"]["values"]["success"]
            elif fault == "missing_enabled":
                del item["observation"]["values"]["enabled"]
            elif fault == "missing_unknown":
                del cohort["values"]["unknown_age"]
            elif fault == "missing_age":
                del cohort["values"]["oldest_known_age_seconds"]
            elif fault == "null_pending":
                cohort["values"]["pending"] = None
            elif fault == "bad_clock":
                cohort["values"]["oldest_boundary_at"] = "not-a-clock"
            elif fault == "snapshot_clocks":
                cohort["observed_at"] = (dt.datetime.fromisoformat(cohort["observed_at"].replace("Z", "+00:00"))-dt.timedelta(seconds=1)).isoformat()
            elif fault == "class_order":
                cohort["class"] = "held"
            elif fault == "bad_counts":
                h["classes"][0]["unknown_age_views"] = 0
            else:
                item["private_error"] = "private-provider-error"
            response["runtime_builds"] = current
            self.build_response_file(response, "single-box-v2")
            result, report = self.invoke()
            self.assertEqual(result.returncode, 0)
            saved = report["runtime_builds"]
            self.assertEqual(saved["handoff_health"], current["handoff_health"])
            self.assertEqual(saved["evidence"], current["evidence"])
            if fault == "unknown_field":
                self.assertIn("app_task_health", saved)
                self.assertNotIn("private-provider-error", json.dumps(saved))
            else:
                self.assertNotIn("app_task_health", saved)
                self.assertEqual(saved["app_task_health_unavailable_reason"], "invalid_app_task_evidence")

    def test_app_task_scope_overlap_and_clipping(self):
        response, current = self.response(), self.build_evidence("single-box-v2")
        self.add_app_task_health(current, "unknown_ages")
        h = current["app_task_health"]
        h["items"].append(copy.deepcopy(h["items"][0]))
        h["items"][1]["target_id"] = "f"*64
        for k in ("observed", "complete", "nonempty_views", "unknown_age_views", "enabled_views", "dispatch_enabled_views", "runner_running_views"):
            h["counts"][k] = 2
        for p in h["classes"]:
            for k in app_task_evidence.CLASS_COUNTS:
                p[k] *= 2
        response["runtime_builds"] = current
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0)
        self.assertEqual(report["runtime_builds"]["app_task_health"], h)
        self.assertNotIn("pending", h["counts"])
        response["padding"] = "x"*((1 << 20)-len(json.dumps(response, indent=2).encode())-1+300)
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0)
        self.assertEqual(report["runtime_builds"]["app_task_health"]["classes"], h["classes"])
        self.assertEqual(report["runtime_builds"]["app_task_health"]["counts"], h["counts"])
        self.assertGreater(report["runtime_builds"]["app_task_envelope_omitted_items"], 0)
        self.assertLessEqual(self.report.stat().st_size, 1 << 20)

    def test_app_task_unknown_class_requires_unknown_timing(self):
        current = self.build_evidence("single-box-v2")
        self.add_app_task_health(current, "nonempty")
        h = current["app_task_health"]
        item = h["items"][0]
        item["classes"][0]["values"], item["classes"][8]["values"] = item["classes"][8]["values"], item["classes"][0]["values"]
        item["classes"][0]["state"], item["classes"][8]["state"] = "empty", "nonempty"
        for key in app_task_evidence.CLASS_COUNTS:
            h["classes"][0][key], h["classes"][8][key] = h["classes"][8][key], h["classes"][0][key]
        with self.assertRaises(ValueError):
            app_task_evidence.project(h, current["evidence"])
        item["classes"][8]["values"] = {"pending": 100000, "unknown_age": 100000}
        self.assertEqual(app_task_evidence.project(h, current["evidence"]), h)
        item["job"] = "imaged"
        with self.assertRaises(ValueError):
            app_task_evidence.project(h, current["evidence"])

    def test_app_task_truncated_history_preserves_prior_nonempty_and_gap(self):
        response = self.history_response(states=("matching", "matching", "unknown", "matching"), ages=(4, 3, 2, 1), role="single-box-v2")
        current = response["runtime_builds"]
        first = dt.datetime.fromisoformat(current["progress"]["first_observed_at"].replace("Z", "+00:00"))
        clocks = [(first+dt.timedelta(seconds=i)).isoformat().replace("+00:00", "Z") for i in range(4)]
        self.add_app_task_health(current)
        h = current["app_task_health"]
        events = [{"at": clocks[2], "observation": 3, "state": "unknown", "reason": "source_server_error", "source_http_status": 503},
                  {"at": clocks[3], "observation": 4, "state": "matching", "evidence_generated_at": clocks[3], "counts": current["evidence"]["counts"], "jobs": current["evidence"]["jobs"],
                   "app_task_health": {k: h[k] for k in ("scope", "state", "counts", "classes", "incomplete")}}]
        p = {"nonempty_transitions": 1, "empty_throughout_observed_polls": False, "observed_incomplete_evidence": True, "observed_unknown_ages": True,
             "first_nonempty_at": clocks[1], "last_nonempty_at": clocks[1], "first_empty_at": clocks[0], "last_empty_at": clocks[3]}
        current["progress"]["app_task_health"] = p
        current["history"].update(events=events, total_events=4, retained_events=2, dropped_events=2, truncated=True, retained_bytes=builds.build_history_bytes(events))
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0)
        self.assertEqual(report["runtime_builds"]["history"], current["history"])
        self.assertEqual(report["runtime_builds"]["progress"], current["progress"])
        p["waiting_transitions"] = p.pop("nonempty_transitions")
        self.build_response_file(response, "single-box-v2")
        _, report = self.invoke()
        self.assertEqual(report["runtime_builds"]["progress"]["app_task_health_unavailable_reason"], "invalid_history")
        self.assertEqual(report["runtime_builds"]["history"], current["history"])

    def add_operation_evidence(self, current, state="errors"):
        self.add_process(current, "baseline" if state == "baseline" else "unchanged")
        e = current["evidence"]
        previous = e["process"].get("comparison_at")
        items = []
        for target in e["items"]:
            if target["job"] not in ("schedd", "schedd-compute"):
                continue
            item = {k: target[k] for k in ("job", "node", "target_id", "scrape_observed_at", "process")}
            item["signals"] = []
            for op in operation_evidence.OPS:
                for outcome in operation_evidence.OUTCOMES:
                    value = {"operation": op, "outcome": outcome, "state": "missing", "reason": "metric_missing"}
                    if outcome != "capacity" or state == "all_zero" or state == "capacity" and op == "restore":
                        value = {"operation": op, "outcome": outcome, "state": "baseline", "current": {"value": 100, "observed_at": e["generated_at"]}}
                        if previous:
                            value.update(state="unchanged", previous={"value": 100, "observed_at": previous}, delta=0)
                            selected = {"errors": "error", "canceled": "canceled", "lease_lost": "lease_lost", "capacity": "capacity"}.get(state)
                            if op == "restore" and outcome == selected:
                                value["current"]["value"], value["delta"], value["state"] = 103, 3, "increased"
                    item["signals"].append(value)
            items.append(item)
        counts, operations = operation_evidence.account(items)
        counts["expected"] = 35*sum(j["expected"] for j in e["jobs"] if j["job"] in ("schedd", "schedd-compute"))
        incomplete = sum(counts[k] for k in operation_evidence.BAD) > 0
        status, reason = ("baseline", "comparison_not_requested") if state == "baseline" else ("increased", "operation_increases_observed") if counts["increased"] else ("incomplete", "incomplete_operation_evidence") if incomplete else ("unchanged", "")
        h = {"scope": operation_evidence.SCOPE, "state": status, "counts": counts, "operations": operations, "incomplete": incomplete, "items": items, "omitted_items": 0}
        if reason:
            h["reason"] = reason
        if previous:
            h["comparison_at"] = previous
        current["app_task_operations"] = h

    def test_operation_outcomes_survive_transport(self):
        for role in ("single-box-v2", "control-plane-v2"):
            for state in ("baseline", "unchanged", "errors", "canceled", "lease_lost", "capacity", "all_zero"):
                with self.subTest(role=role, state=state):
                    response, current = self.response(), self.build_evidence(role)
                    self.add_app_task_health(current)
                    self.add_command_cron_health(current)
                    self.add_operation_evidence(current, state)
                    response["runtime_builds"] = current
                    self.build_response_file(response, role)
                    result, saved = self.invoke()
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertEqual(saved["runtime_builds"], current)
                    self.assertEqual(saved["checks"], response["checks"])
                    summary = (self.root/"summary").read_text()
                    self.assertIn("App-task operation outcomes (advisory)", summary)
                    self.assertNotIn("operator-only-node", summary)
                    self.assertNotIn("103", summary)
                    h = saved["runtime_builds"]["app_task_operations"]
                    if state == "baseline":
                        self.assertEqual(h["counts"]["increased"], 0)
                    if state != "all_zero":
                        self.assertGreater(h["counts"]["missing"], 0)

    def test_invalid_operation_evidence_is_isolated(self):
        for fault in ("missing_scope", "wrong_operation", "missing_value", "null_value", "missing_delta", "forged_delta", "clock_mismatch", "uncertain_window", "counter_reset", "bad_summary", "wrong_window", "unknown_field"):
            with self.subTest(fault=fault):
                response, current = self.response(), self.build_evidence("single-box-v2")
                self.add_app_task_health(current)
                self.add_operation_evidence(current)
                h = current["app_task_operations"]
                point = h["items"][0]["signals"][1]
                if fault == "missing_scope":
                    h.pop("scope")
                elif fault == "wrong_operation":
                    point["operation"] = "arbitrary"
                elif fault == "missing_value":
                    point["current"].pop("value")
                elif fault == "null_value":
                    point["current"]["value"] = None
                elif fault == "missing_delta":
                    point.pop("delta")
                elif fault == "forged_delta":
                    point["delta"] = 900
                elif fault == "clock_mismatch":
                    point["current"]["observed_at"] = (dt.datetime.fromisoformat(point["current"]["observed_at"].replace("Z", "+00:00"))-dt.timedelta(seconds=1)).isoformat()
                elif fault == "uncertain_window":
                    h["items"][0]["process"]["uptime_seconds"] = 110
                elif fault == "counter_reset":
                    point["previous"]["value"] = 104
                elif fault == "bad_summary":
                    h["operations"][0]["error_increases"] = 0
                elif fault == "wrong_window":
                    h["comparison_at"] = current["evidence"]["generated_at"]
                else:
                    point["private_error"] = "private-provider-error"
                response["runtime_builds"] = current
                self.build_response_file(response, "single-box-v2")
                result, saved = self.invoke()
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(saved["runtime_builds"]["app_task_health"], current["app_task_health"])
                self.assertEqual(saved["runtime_builds"]["evidence"], current["evidence"])
                if fault == "unknown_field":
                    self.assertIn("app_task_operations", saved["runtime_builds"])
                else:
                    self.assertNotIn("app_task_operations", saved["runtime_builds"])
                    self.assertEqual(saved["runtime_builds"]["app_task_operations_unavailable_reason"], "invalid_operation_evidence")

    def test_operation_preview_yields_before_other_evidence(self):
        response, current = self.response(), self.build_evidence("single-box-v2")
        self.add_operation_evidence(current)
        response["runtime_builds"] = current
        response["padding"] = "x"*((1 << 20)-len(json.dumps(response, indent=2).encode())-1+300)
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(report["runtime_builds"]["app_task_operations"]["counts"], current["app_task_operations"]["counts"])
        self.assertEqual(report["runtime_builds"]["app_task_operations_envelope_omitted_items"], 1)
        self.assertEqual(report["runtime_builds"]["evidence"], current["evidence"])

    def test_operation_history_keeps_error_after_gap_and_clipping(self):
        response = self.history_response(states=("matching", "unknown", "matching"), ages=(3, 2, 1), role="single-box-v2")
        current = response["runtime_builds"]
        self.add_operation_evidence(current, "unchanged")
        progress = current["progress"]
        first, last = progress["first_observed_at"], progress["last_observed_at"]
        gap = (dt.datetime.fromisoformat(last.replace("Z", "+00:00"))-dt.timedelta(seconds=1)).isoformat().replace("+00:00", "Z")
        h = current["app_task_operations"]
        events = [{"at": gap, "observation": 2, "state": "unknown", "reason": "source_server_error", "source_http_status": 503},
                  {"at": last, "observation": 3, "state": "matching", "evidence_generated_at": current["evidence"]["generated_at"], "counts": current["evidence"]["counts"], "jobs": current["evidence"]["jobs"], "process": current["evidence"]["process"],
                   "app_task_operations": {k: h[k] for k in ("scope", "state", "reason", "counts", "operations", "incomplete") if k in h}}]
        p = dict.fromkeys(operation_evidence.BOOLS, False)
        p.update(observed_increases=True, observed_errors=True, observed_incomplete_evidence=True, first_increase_at=first, last_increase_at=first)
        progress["app_task_operations"] = p
        current["history"].update(events=events, retained_events=2, total_events=3, dropped_events=1, truncated=True, retained_bytes=builds.build_history_bytes(events))
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(report["runtime_builds"]["history"], current["history"])
        self.assertEqual(report["runtime_builds"]["progress"], current["progress"])
        p["observed_increases"] = False
        self.build_response_file(response, "single-box-v2")
        _, report = self.invoke()
        self.assertEqual(report["runtime_builds"]["progress"]["app_task_operations_unavailable_reason"], "invalid_history")
        self.assertEqual(report["runtime_builds"]["history"], current["history"])

    def add_result_evidence(self, current, state="failed"):
        self.add_process(current, "baseline" if state == "baseline" else "unchanged")
        e = current["evidence"]
        previous = e["process"].get("comparison_at")
        items = []
        for target in e["items"]:
            if target["job"] not in ("schedd", "schedd-compute"):
                continue
            item = {k: copy.deepcopy(target[k]) for k in ("job", "node", "target_id", "scrape_observed_at", "process")}
            item["signals"] = []
            for i, d in enumerate(result_evidence.DIMENSIONS):
                value = {**d, "state": "baseline", "current": {"value": 100, "observed_at": e["generated_at"]}}
                if previous:
                    value.update(state="unchanged", previous={"value": 100, "observed_at": previous}, delta=0)
                    selected = state if state not in ("sparse", "recovery_cancelled") else "failed" if state == "sparse" else "cancelled"
                    source = "recovery" if state in ("requeued_restores", "failed_runs", "recovery_cancelled") else "completion"
                    if d["source"] == source and d["outcome"] == selected:
                        value["current"]["value"], value["delta"], value["state"] = 103, 3, "increased"
                if state == "sparse" and i == 15:
                    value = {**d, "state": "missing", "reason": "metric_missing"}
                item["signals"].append(value)
            items.append(item)
        counts, outcomes = result_evidence.account(items)
        counts["expected"] = 18*sum(j["expected"] for j in e["jobs"] if j["job"] in ("schedd", "schedd-compute"))
        incomplete = sum(counts[k] for k in operation_evidence.BAD) > 0
        status, reason = ("baseline", "comparison_not_requested") if state == "baseline" else ("increased", "result_increases_observed") if counts["increased"] else ("incomplete", "incomplete_result_evidence") if incomplete else ("unchanged", "")
        h = {"scope": operation_evidence.SCOPE, "state": status, "counts": counts, "outcomes": outcomes, "incomplete": incomplete, "items": items, "omitted_items": 0}
        if reason:
            h["reason"] = reason
        if previous:
            h["comparison_at"] = previous
        current["app_task_results"] = h

    def test_result_outcomes_survive_transport(self):
        for role in ("single-box-v2", "control-plane-v2"):
            for state in ("baseline", "unchanged", "succeeded", "failed", "timed_out", "cancelled", "queued", "requeued_restores", "failed_runs", "recovery_cancelled", "sparse"):
                with self.subTest(role=role, state=state):
                    response, current = self.response(), self.build_evidence(role)
                    self.add_app_task_health(current)
                    self.add_command_cron_health(current)
                    self.add_result_evidence(current, state)
                    response["runtime_builds"] = current
                    self.build_response_file(response, role)
                    result, saved = self.invoke()
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertEqual(saved["runtime_builds"], current)
                    self.assertEqual(saved["checks"], response["checks"])
                    summary = (self.root/"summary").read_text()
                    self.assertIn("App-task completion and recovery (advisory)", summary)
                    self.assertNotIn("operator-only-node", summary)
                    self.assertNotIn("103", summary)
                    h = saved["runtime_builds"]["app_task_results"]
                    if state == "baseline":
                        self.assertEqual(h["counts"]["increased"], 0)
                    if state == "sparse":
                        self.assertGreater(h["counts"]["missing"], 0)
                        self.assertGreater(h["counts"]["increased"], 0)

    def test_invalid_result_evidence_is_isolated(self):
        for fault in ("missing_scope", "wrong_kind", "missing_value", "null_value", "missing_delta", "forged_delta", "clock_mismatch", "uncertain_window", "counter_reset", "bad_summary", "wrong_window", "unknown_field"):
            with self.subTest(fault=fault):
                response, current = self.response(), self.build_evidence("single-box-v2")
                self.add_app_task_health(current)
                self.add_result_evidence(current)
                h = current["app_task_results"]
                point = h["items"][0]["signals"][1]
                if fault == "missing_scope":
                    h.pop("scope")
                elif fault == "wrong_kind":
                    point["kind"] = "arbitrary"
                elif fault == "missing_value":
                    point["current"].pop("value")
                elif fault == "null_value":
                    point["current"]["value"] = None
                elif fault == "missing_delta":
                    point.pop("delta")
                elif fault == "forged_delta":
                    point["delta"] = 900
                elif fault == "clock_mismatch":
                    point["current"]["observed_at"] = (dt.datetime.fromisoformat(point["current"]["observed_at"].replace("Z", "+00:00"))-dt.timedelta(seconds=1)).isoformat()
                elif fault == "uncertain_window":
                    h["items"][0]["process"]["uptime_seconds"] = 110
                elif fault == "counter_reset":
                    point["previous"]["value"] = 104
                elif fault == "bad_summary":
                    h["outcomes"][1]["increased"] = 0
                elif fault == "wrong_window":
                    h["comparison_at"] = current["evidence"]["generated_at"]
                else:
                    point["private_error"] = "private-provider-error"
                response["runtime_builds"] = current
                self.build_response_file(response, "single-box-v2")
                result, saved = self.invoke()
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(saved["runtime_builds"]["app_task_health"], current["app_task_health"])
                self.assertEqual(saved["runtime_builds"]["evidence"], current["evidence"])
                if fault == "unknown_field":
                    self.assertIn("app_task_results", saved["runtime_builds"])
                else:
                    self.assertNotIn("app_task_results", saved["runtime_builds"])
                    self.assertEqual(saved["runtime_builds"]["app_task_results_unavailable_reason"], "invalid_result_evidence")

    def test_result_preview_yields_before_other_evidence(self):
        response, current = self.response(), self.build_evidence("single-box-v2")
        self.add_result_evidence(current)
        response["runtime_builds"] = current
        response["padding"] = "x"*((1 << 20)-len(json.dumps(response, indent=2).encode())-1+300)
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(report["runtime_builds"]["app_task_results"]["counts"], current["app_task_results"]["counts"])
        self.assertEqual(report["runtime_builds"]["app_task_results_envelope_omitted_items"], 1)
        self.assertEqual(report["runtime_builds"]["evidence"], current["evidence"])

    def test_result_history_keeps_error_after_gap_and_clipping(self):
        response = self.history_response(states=("matching", "unknown", "matching"), ages=(3, 2, 1), role="single-box-v2")
        current = response["runtime_builds"]
        self.add_result_evidence(current, "unchanged")
        progress = current["progress"]
        first, last = progress["first_observed_at"], progress["last_observed_at"]
        gap = (dt.datetime.fromisoformat(last.replace("Z", "+00:00"))-dt.timedelta(seconds=1)).isoformat().replace("+00:00", "Z")
        h = current["app_task_results"]
        events = [{"at": gap, "observation": 2, "state": "unknown", "reason": "source_server_error", "source_http_status": 503},
                  {"at": last, "observation": 3, "state": "matching", "evidence_generated_at": current["evidence"]["generated_at"], "counts": current["evidence"]["counts"], "jobs": current["evidence"]["jobs"], "process": current["evidence"]["process"],
                   "app_task_results": {k: h[k] for k in ("scope", "state", "reason", "counts", "outcomes", "incomplete") if k in h}}]
        p = dict.fromkeys(result_evidence.BOOLS, False)
        p.update(observed_increases=True, observed_failed=True, observed_incomplete_evidence=True, first_increase_at=first, last_increase_at=first)
        progress["app_task_results"] = p
        current["history"].update(events=events, retained_events=2, total_events=3, dropped_events=1, truncated=True, retained_bytes=builds.build_history_bytes(events))
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(report["runtime_builds"]["history"], current["history"])
        self.assertEqual(report["runtime_builds"]["progress"], current["progress"])
        p["observed_increases"] = False
        self.build_response_file(response, "single-box-v2")
        _, report = self.invoke()
        self.assertEqual(report["runtime_builds"]["progress"]["app_task_results_unavailable_reason"], "invalid_history")
        self.assertEqual(report["runtime_builds"]["history"], current["history"])

    def add_cron_timing_evidence(self, current, state="timed"):
        self.add_process(current, "baseline" if state == "baseline" else "unchanged")
        e = current["evidence"]
        previous = e["process"].get("comparison_at")
        items = []
        for target in e["items"]:
            if target["job"] not in ("schedd", "schedd-compute"):
                continue
            item = {k: copy.deepcopy(target[k]) for k in ("job", "node", "target_id", "scrape_observed_at", "process")}
            item["signals"] = []
            for i, d in enumerate(cron_timing_evidence.DIMENSIONS):
                value = {**d, "state": "baseline", "current": {"value": 100, "observed_at": e["generated_at"]}}
                if previous:
                    value.update(state="unchanged", previous={"value": 100, "observed_at": previous}, delta=0)
                    if state == "timed" and 4 <= i <= 11 or state in ("unknown", "sparse") and i == 12:
                        value["current"]["value"], value["delta"], value["state"] = 103, 3, "increased"
                if state == "sparse" and i < 12:
                    value = {**d, "state": "missing", "reason": "metric_missing"}
                item["signals"].append(value)
            items.append(item)
        counts, outcomes = cron_timing_evidence.account(items)
        counts["expected"] = 13*sum(j["expected"] for j in e["jobs"] if j["job"] in ("schedd", "schedd-compute"))
        incomplete = sum(counts[k] for k in operation_evidence.BAD) > 0
        status, reason = ("baseline", "comparison_not_requested") if state == "baseline" else ("increased", "cron_timing_increases_observed") if counts["increased"] else ("incomplete", "incomplete_cron_timing_evidence") if incomplete else ("unchanged", "")
        h = {"scope": operation_evidence.SCOPE, "state": status, "counts": counts, "signals": outcomes, "incomplete": incomplete, "items": items, "omitted_items": 0}
        if reason:
            h["reason"] = reason
        if previous:
            h["comparison_at"] = previous
        current["command_cron_timing"] = h

    def test_cron_timing_transport_and_histogram_isolation(self):
        for role in ("single-box-v2", "control-plane-v2"):
            for state in ("baseline", "unchanged", "timed", "unknown", "sparse"):
                with self.subTest(role=role, state=state):
                    response, current = self.response(), self.build_evidence(role)
                    self.add_app_task_health(current)
                    self.add_cron_timing_evidence(current, state)
                    response["runtime_builds"] = current
                    self.build_response_file(response, role)
                    result, saved = self.invoke()
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertEqual(saved["runtime_builds"], current)
                    self.assertEqual(saved["checks"], response["checks"])
                    summary = (self.root/"summary").read_text()
                    self.assertIn("Command-cron admission timing (advisory)", summary)
                    self.assertNotIn("operator-only-node", summary)
                    self.assertNotIn("103", summary)
                    h = saved["runtime_builds"]["command_cron_timing"]
                    self.assertEqual(h["signals"][-1]["increased"], len(h["items"]) if state in ("unknown", "sparse") else 0)

    def test_cron_timing_preview_yields_before_existing_evidence(self):
        response, current = self.response(), self.build_evidence("single-box-v2")
        self.add_cron_outcome_evidence(current)
        self.add_cron_timing_evidence(current)
        response["runtime_builds"] = current
        response["padding"] = "x"*((1 << 20)-len(json.dumps(response, indent=2).encode())-1+300)
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(report["runtime_builds"]["command_cron_timing"]["counts"], current["command_cron_timing"]["counts"])
        self.assertEqual(report["runtime_builds"]["command_cron_timing_envelope_omitted_items"], 1)
        self.assertEqual(report["runtime_builds"]["command_cron_outcomes"], current["command_cron_outcomes"])
        self.assertEqual(report["runtime_builds"]["evidence"], current["evidence"])

    def test_invalid_cron_timing_histogram_is_isolated(self):
        for fault in ("partial", "nonmonotonic", "count_mismatch", "impossible_delta", "clock_mismatch", "forged_delta", "wrong_signal", "missing_value", "missing_delta", "counter_reset", "unknown_field"):
            with self.subTest(fault=fault):
                response, current = self.response(), self.build_evidence("single-box-v2")
                self.add_app_task_health(current)
                self.add_cron_timing_evidence(current)
                h = current["command_cron_timing"]
                signals = h["items"][0]["signals"]
                if fault == "partial":
                    signals[0].pop("current")
                    signals[0].update(state="missing", reason="metric_missing")
                    signals[0].pop("previous"); signals[0].pop("delta")
                    # Correct the signal summaries: incoherence alone rejects it.
                    h["signals"][0].update(unchanged=0, unknown=1)
                    h["counts"]["observed"] -= 1; h["counts"]["unchanged"] -= 1; h["counts"]["missing"] += 1
                    h["incomplete"] = True
                elif fault == "nonmonotonic":
                    signals[0]["current"]["value"] = 104
                    signals[0].update(state="increased", delta=4)
                elif fault == "count_mismatch":
                    signals[11]["current"]["value"] = 104; signals[11]["delta"] = 4
                elif fault == "impossible_delta":
                    # Valid endpoint histograms, impossible distribution of deltas.
                    for signal in signals[1:12]:
                        signal["previous"]["value"] -= 2
                        signal["delta"] += 2
                    signals[0]["previous"]["value"] -= 3
                    signals[0].update(state="increased", delta=3)
                elif fault == "clock_mismatch":
                    signals[0]["current"]["observed_at"] = h["comparison_at"]
                elif fault == "forged_delta": signals[0]["delta"] = 1
                elif fault == "wrong_signal": signals[0]["signal"] = "arbitrary"
                elif fault == "missing_value": signals[0]["current"].pop("value")
                elif fault == "missing_delta": signals[0].pop("delta")
                elif fault == "counter_reset": signals[0]["previous"]["value"] = 104
                else: signals[0]["private_error"] = "private-provider-error"
                if fault in ("nonmonotonic", "impossible_delta"):
                    c, categories = cron_timing_evidence.account(h["items"])
                    c["expected"] = h["counts"]["expected"]
                    h["counts"], h["signals"] = c, categories
                response["runtime_builds"] = current
                self.build_response_file(response, "single-box-v2")
                result, saved = self.invoke()
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(saved["runtime_builds"]["evidence"], current["evidence"])
                self.assertEqual(saved["runtime_builds"]["app_task_health"], current["app_task_health"])
                if fault == "unknown_field":
                    self.assertIn("command_cron_timing", saved["runtime_builds"])
                    self.assertNotIn("private-provider-error", json.dumps(saved))
                else:
                    self.assertNotIn("command_cron_timing", saved["runtime_builds"])
                    self.assertEqual(saved["runtime_builds"]["command_cron_timing_unavailable_reason"], "invalid_cron_timing_evidence")

    def add_delayed_task_dispatch_evidence(self, current, state="failed"):
        self.add_process(current, "baseline" if state == "baseline" else "unchanged")
        e = current["evidence"]
        previous = e["process"].get("comparison_at")
        items = []
        for target in e["items"]:
            if target["job"] not in ("schedd", "schedd-compute"):
                continue
            item = {k: copy.deepcopy(target[k]) for k in ("job", "node", "target_id", "scrape_observed_at", "process")}
            item["signals"] = []
            for i, d in enumerate(delayed_task_dispatch_evidence.DIMENSIONS):
                value = {**d, "state": "baseline", "current": {"value": 100, "observed_at": e["generated_at"]}}
                if previous:
                    value.update(state="unchanged", previous={"value": 100, "observed_at": previous}, delta=0)
                    selected = "failed" if state == "sparse" else state
                    if d["outcome"] == selected:
                        value["current"]["value"], value["delta"], value["state"] = 103, 3, "increased"
                if state == "sparse" and i == 0:
                    value = {**d, "state": "missing", "reason": "metric_missing"}
                item["signals"].append(value)
            items.append(item)
        counts, outcomes = delayed_task_dispatch_evidence.account(items)
        counts["expected"] = 4*sum(j["expected"] for j in e["jobs"] if j["job"] in ("schedd", "schedd-compute"))
        incomplete = sum(counts[k] for k in operation_evidence.BAD) > 0
        status, reason = ("baseline", "comparison_not_requested") if state == "baseline" else ("increased", "delayed_task_dispatch_increases_observed") if counts["increased"] else ("incomplete", "incomplete_delayed_task_dispatch_evidence") if incomplete else ("unchanged", "")
        h = {"scope": operation_evidence.SCOPE, "state": status, "counts": counts, "outcomes": outcomes, "incomplete": incomplete, "items": items, "omitted_items": 0}
        if reason:
            h["reason"] = reason
        if previous:
            h["comparison_at"] = previous
        current["delayed_task_dispatch"] = h

    def test_delayed_task_dispatch_outcomes_survive_transport(self):
        for role in ("single-box-v2", "control-plane-v2"):
            for state in ("baseline", "unchanged", "sparse", *(d["outcome"] for d in delayed_task_dispatch_evidence.DIMENSIONS)):
                with self.subTest(role=role, state=state):
                    response, current = self.response(), self.build_evidence(role)
                    self.add_app_task_health(current)
                    self.add_command_cron_health(current)
                    self.add_delayed_task_dispatch_evidence(current, state)
                    response["runtime_builds"] = current
                    self.build_response_file(response, role)
                    result, saved = self.invoke()
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertEqual(saved["runtime_builds"], current)
                    self.assertEqual(saved["checks"], response["checks"])
                    summary = (self.root/"summary").read_text()
                    self.assertIn("Delayed-task dispatch outcomes (advisory)", summary)
                    self.assertNotIn("operator-only-node", summary)
                    self.assertNotIn("103", summary)
                    h = saved["runtime_builds"]["delayed_task_dispatch"]
                    if state == "baseline":
                        self.assertEqual(h["counts"]["increased"], 0)
                    if state == "sparse":
                        self.assertGreater(h["counts"]["missing"], 0)
                        self.assertGreater(h["counts"]["increased"], 0)

    def test_invalid_delayed_task_dispatch_evidence_is_isolated(self):
        for fault in ("missing_scope", "wrong_outcome", "missing_value", "null_value", "missing_delta", "forged_delta", "clock_mismatch", "uncertain_window", "counter_reset", "bad_summary", "wrong_window", "unknown_field"):
            with self.subTest(fault=fault):
                response, current = self.response(), self.build_evidence("single-box-v2")
                self.add_app_task_health(current)
                self.add_delayed_task_dispatch_evidence(current)
                h = current["delayed_task_dispatch"]
                point = h["items"][0]["signals"][2]
                if fault == "missing_scope":
                    h.pop("scope")
                elif fault == "wrong_outcome":
                    point["outcome"] = "arbitrary"
                elif fault == "missing_value":
                    point["current"].pop("value")
                elif fault == "null_value":
                    point["current"]["value"] = None
                elif fault == "missing_delta":
                    point.pop("delta")
                elif fault == "forged_delta":
                    point["delta"] = 900
                elif fault == "clock_mismatch":
                    point["current"]["observed_at"] = (dt.datetime.fromisoformat(point["current"]["observed_at"].replace("Z", "+00:00"))-dt.timedelta(seconds=1)).isoformat()
                elif fault == "uncertain_window":
                    h["items"][0]["process"]["uptime_seconds"] = 110
                elif fault == "counter_reset":
                    point["previous"]["value"] = 104
                elif fault == "bad_summary":
                    h["outcomes"][2]["increased"] = 0
                elif fault == "wrong_window":
                    h["comparison_at"] = current["evidence"]["generated_at"]
                else:
                    point["private_error"] = "private-provider-error"
                response["runtime_builds"] = current
                self.build_response_file(response, "single-box-v2")
                result, saved = self.invoke()
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(saved["runtime_builds"]["app_task_health"], current["app_task_health"])
                self.assertEqual(saved["runtime_builds"]["evidence"], current["evidence"])
                if fault == "unknown_field":
                    self.assertIn("delayed_task_dispatch", saved["runtime_builds"])
                else:
                    self.assertNotIn("delayed_task_dispatch", saved["runtime_builds"])
                    self.assertEqual(saved["runtime_builds"]["delayed_task_dispatch_unavailable_reason"], "invalid_delayed_task_dispatch_evidence")

    def test_delayed_task_dispatch_selected_view(self):
        response, current = self.response(), self.build_evidence("single-box-v2")
        self.add_http_cron_delivery_evidence(current)
        self.add_delayed_task_dispatch_evidence(current, "retry")
        wanted = diagnostic_selection.parse("single-box-v2", "delayed_task_dispatch")
        current.update(diagnostics=wanted, diagnostics_source="server")
        response["runtime_builds"] = current
        self.env["MONITORING_RUNTIME_DIAGNOSTICS"] = "delayed_task_dispatch"
        self.build_response_file(response, "single-box-v2")
        result, saved = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        out = saved["runtime_builds"]
        self.assertEqual(out["delayed_task_dispatch"], current["delayed_task_dispatch"])
        self.assertNotIn("http_cron_delivery", out)
        self.assertEqual(out["diagnostics"], wanted)
        self.assertEqual(saved["checks"], response["checks"])
        summary = (self.root/"summary").read_text()
        self.assertIn("Failed includes application errors", summary)
        self.assertNotIn("HTTP cron delivery outcomes (advisory)", summary)

    def test_delayed_task_dispatch_preview_preserves_existing_sections(self):
        response, current = self.response(), self.build_evidence("single-box-v2")
        self.add_http_cron_delivery_evidence(current)
        self.add_delayed_task_dispatch_evidence(current)
        response["runtime_builds"] = current
        response["padding"] = "x"*((1 << 20)-len(json.dumps(response, indent=2).encode())-1+300)
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(report["runtime_builds"]["delayed_task_dispatch_envelope_omitted_items"], 1)
        self.assertEqual(report["runtime_builds"]["delayed_task_dispatch"]["counts"], current["delayed_task_dispatch"]["counts"])
        self.assertEqual(report["runtime_builds"]["http_cron_delivery"], current["http_cron_delivery"])
        self.assertEqual(report["runtime_builds"]["evidence"], current["evidence"])

    def test_delayed_task_dispatch_preview_yields_before_other_evidence(self):
        response, current = self.response(), self.build_evidence("single-box-v2")
        self.add_delayed_task_dispatch_evidence(current)
        response["runtime_builds"] = current
        response["padding"] = "x"*((1 << 20)-len(json.dumps(response, indent=2).encode())-1+300)
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(report["runtime_builds"]["delayed_task_dispatch"]["counts"], current["delayed_task_dispatch"]["counts"])
        self.assertEqual(report["runtime_builds"]["delayed_task_dispatch_envelope_omitted_items"], 1)
        self.assertEqual(report["runtime_builds"]["evidence"], current["evidence"])

    def test_delayed_task_dispatch_history_keeps_error_after_gap_and_clipping(self):
        response = self.history_response(states=("matching", "unknown", "matching"), ages=(3, 2, 1), role="single-box-v2")
        current = response["runtime_builds"]
        self.add_delayed_task_dispatch_evidence(current, "unchanged")
        progress = current["progress"]
        first, last = progress["first_observed_at"], progress["last_observed_at"]
        gap = (dt.datetime.fromisoformat(last.replace("Z", "+00:00"))-dt.timedelta(seconds=1)).isoformat().replace("+00:00", "Z")
        h = current["delayed_task_dispatch"]
        events = [{"at": gap, "observation": 2, "state": "unknown", "reason": "source_server_error", "source_http_status": 503},
                  {"at": last, "observation": 3, "state": "matching", "evidence_generated_at": current["evidence"]["generated_at"], "counts": current["evidence"]["counts"], "jobs": current["evidence"]["jobs"], "process": current["evidence"]["process"],
                   "delayed_task_dispatch": {k: h[k] for k in ("scope", "state", "reason", "counts", "outcomes", "incomplete") if k in h}}]
        p = dict.fromkeys(delayed_task_dispatch_evidence.BOOLS, False)
        p.update(observed_increases=True, observed_outcomes=[{"outcome":name} for name in ("retry", "failed", "dead_letter")], observed_incomplete_evidence=True, first_increase_at=first, last_increase_at=first)
        progress["delayed_task_dispatch"] = p
        current["history"].update(events=events, retained_events=2, total_events=3, dropped_events=1, truncated=True, retained_bytes=builds.build_history_bytes(events))
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(report["runtime_builds"]["history"], current["history"])
        self.assertEqual(report["runtime_builds"]["progress"], current["progress"])
        p["observed_increases"] = False
        self.build_response_file(response, "single-box-v2")
        _, report = self.invoke()
        self.assertEqual(report["runtime_builds"]["progress"]["delayed_task_dispatch_unavailable_reason"], "invalid_history")
        self.assertEqual(report["runtime_builds"]["history"], current["history"])

    def add_http_cron_delivery_evidence(self, current, state="failed"):
        self.add_process(current, "baseline" if state == "baseline" else "unchanged")
        e = current["evidence"]
        previous = e["process"].get("comparison_at")
        items = []
        for target in e["items"]:
            if target["job"] not in ("schedd", "schedd-compute"):
                continue
            item = {k: copy.deepcopy(target[k]) for k in ("job", "node", "target_id", "scrape_observed_at", "process")}
            item["signals"] = []
            for i, d in enumerate(http_cron_delivery_evidence.DIMENSIONS):
                value = {**d, "state": "baseline", "current": {"value": 100, "observed_at": e["generated_at"]}}
                if previous:
                    value.update(state="unchanged", previous={"value": 100, "observed_at": previous}, delta=0)
                    selected = "failed" if state == "sparse" else state
                    if d["outcome"] == selected:
                        value["current"]["value"], value["delta"], value["state"] = 103, 3, "increased"
                if state == "sparse" and i == 0:
                    value = {**d, "state": "missing", "reason": "metric_missing"}
                item["signals"].append(value)
            items.append(item)
        counts, outcomes = http_cron_delivery_evidence.account(items)
        counts["expected"] = 5*sum(j["expected"] for j in e["jobs"] if j["job"] in ("schedd", "schedd-compute"))
        incomplete = sum(counts[k] for k in operation_evidence.BAD) > 0
        status, reason = ("baseline", "comparison_not_requested") if state == "baseline" else ("increased", "http_cron_delivery_increases_observed") if counts["increased"] else ("incomplete", "incomplete_http_cron_delivery_evidence") if incomplete else ("unchanged", "")
        h = {"scope": operation_evidence.SCOPE, "state": status, "counts": counts, "outcomes": outcomes, "incomplete": incomplete, "items": items, "omitted_items": 0}
        if reason:
            h["reason"] = reason
        if previous:
            h["comparison_at"] = previous
        current["http_cron_delivery"] = h

    def test_http_cron_delivery_outcomes_survive_transport(self):
        for role in ("single-box-v2", "control-plane-v2"):
            for state in ("baseline", "unchanged", "sparse", *(d["outcome"] for d in http_cron_delivery_evidence.DIMENSIONS)):
                with self.subTest(role=role, state=state):
                    response, current = self.response(), self.build_evidence(role)
                    self.add_app_task_health(current)
                    self.add_command_cron_health(current)
                    self.add_http_cron_delivery_evidence(current, state)
                    response["runtime_builds"] = current
                    self.build_response_file(response, role)
                    result, saved = self.invoke()
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertEqual(saved["runtime_builds"], current)
                    self.assertEqual(saved["checks"], response["checks"])
                    summary = (self.root/"summary").read_text()
                    self.assertIn("HTTP cron delivery outcomes (advisory)", summary)
                    self.assertNotIn("operator-only-node", summary)
                    self.assertNotIn("103", summary)
                    h = saved["runtime_builds"]["http_cron_delivery"]
                    if state == "baseline":
                        self.assertEqual(h["counts"]["increased"], 0)
                    if state == "sparse":
                        self.assertGreater(h["counts"]["missing"], 0)
                        self.assertGreater(h["counts"]["increased"], 0)

    def test_invalid_http_cron_delivery_evidence_is_isolated(self):
        for fault in ("missing_scope", "wrong_outcome", "missing_value", "null_value", "missing_delta", "forged_delta", "clock_mismatch", "uncertain_window", "counter_reset", "bad_summary", "wrong_window", "unknown_field"):
            with self.subTest(fault=fault):
                response, current = self.response(), self.build_evidence("single-box-v2")
                self.add_app_task_health(current)
                self.add_http_cron_delivery_evidence(current)
                h = current["http_cron_delivery"]
                point = h["items"][0]["signals"][3]
                if fault == "missing_scope":
                    h.pop("scope")
                elif fault == "wrong_outcome":
                    point["outcome"] = "arbitrary"
                elif fault == "missing_value":
                    point["current"].pop("value")
                elif fault == "null_value":
                    point["current"]["value"] = None
                elif fault == "missing_delta":
                    point.pop("delta")
                elif fault == "forged_delta":
                    point["delta"] = 900
                elif fault == "clock_mismatch":
                    point["current"]["observed_at"] = (dt.datetime.fromisoformat(point["current"]["observed_at"].replace("Z", "+00:00"))-dt.timedelta(seconds=1)).isoformat()
                elif fault == "uncertain_window":
                    h["items"][0]["process"]["uptime_seconds"] = 110
                elif fault == "counter_reset":
                    point["previous"]["value"] = 104
                elif fault == "bad_summary":
                    h["outcomes"][3]["increased"] = 0
                elif fault == "wrong_window":
                    h["comparison_at"] = current["evidence"]["generated_at"]
                else:
                    point["private_error"] = "private-provider-error"
                response["runtime_builds"] = current
                self.build_response_file(response, "single-box-v2")
                result, saved = self.invoke()
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(saved["runtime_builds"]["app_task_health"], current["app_task_health"])
                self.assertEqual(saved["runtime_builds"]["evidence"], current["evidence"])
                if fault == "unknown_field":
                    self.assertIn("http_cron_delivery", saved["runtime_builds"])
                else:
                    self.assertNotIn("http_cron_delivery", saved["runtime_builds"])
                    self.assertEqual(saved["runtime_builds"]["http_cron_delivery_unavailable_reason"], "invalid_http_cron_delivery_evidence")

    def test_http_cron_delivery_preview_preserves_existing_sections(self):
        response, current = self.response(), self.build_evidence("single-box-v2")
        self.add_cron_timing_evidence(current)
        self.add_http_cron_delivery_evidence(current)
        response["runtime_builds"] = current
        response["padding"] = "x"*((1 << 20)-len(json.dumps(response, indent=2).encode())-1+300)
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(report["runtime_builds"]["http_cron_delivery_envelope_omitted_items"], 1)
        self.assertEqual(report["runtime_builds"]["http_cron_delivery"]["counts"], current["http_cron_delivery"]["counts"])
        self.assertEqual(report["runtime_builds"]["command_cron_timing"], current["command_cron_timing"])
        self.assertEqual(report["runtime_builds"]["evidence"], current["evidence"])

    def test_http_cron_delivery_preview_yields_before_other_evidence(self):
        response, current = self.response(), self.build_evidence("single-box-v2")
        self.add_http_cron_delivery_evidence(current)
        response["runtime_builds"] = current
        response["padding"] = "x"*((1 << 20)-len(json.dumps(response, indent=2).encode())-1+300)
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(report["runtime_builds"]["http_cron_delivery"]["counts"], current["http_cron_delivery"]["counts"])
        self.assertEqual(report["runtime_builds"]["http_cron_delivery_envelope_omitted_items"], 1)
        self.assertEqual(report["runtime_builds"]["evidence"], current["evidence"])

    def test_http_cron_delivery_history_keeps_error_after_gap_and_clipping(self):
        response = self.history_response(states=("matching", "unknown", "matching"), ages=(3, 2, 1), role="single-box-v2")
        current = response["runtime_builds"]
        self.add_http_cron_delivery_evidence(current, "unchanged")
        progress = current["progress"]
        first, last = progress["first_observed_at"], progress["last_observed_at"]
        gap = (dt.datetime.fromisoformat(last.replace("Z", "+00:00"))-dt.timedelta(seconds=1)).isoformat().replace("+00:00", "Z")
        h = current["http_cron_delivery"]
        events = [{"at": gap, "observation": 2, "state": "unknown", "reason": "source_server_error", "source_http_status": 503},
                  {"at": last, "observation": 3, "state": "matching", "evidence_generated_at": current["evidence"]["generated_at"], "counts": current["evidence"]["counts"], "jobs": current["evidence"]["jobs"], "process": current["evidence"]["process"],
                   "http_cron_delivery": {k: h[k] for k in ("scope", "state", "reason", "counts", "outcomes", "incomplete") if k in h}}]
        p = dict.fromkeys(http_cron_delivery_evidence.BOOLS, False)
        p.update(observed_increases=True, observed_outcomes=[{"outcome":name} for name in ("retry", "app_error", "failed", "dead_letter")], observed_incomplete_evidence=True, first_increase_at=first, last_increase_at=first)
        progress["http_cron_delivery"] = p
        current["history"].update(events=events, retained_events=2, total_events=3, dropped_events=1, truncated=True, retained_bytes=builds.build_history_bytes(events))
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(report["runtime_builds"]["history"], current["history"])
        self.assertEqual(report["runtime_builds"]["progress"], current["progress"])
        p["observed_increases"] = False
        self.build_response_file(response, "single-box-v2")
        _, report = self.invoke()
        self.assertEqual(report["runtime_builds"]["progress"]["http_cron_delivery_unavailable_reason"], "invalid_history")
        self.assertEqual(report["runtime_builds"]["history"], current["history"])

    def add_cron_outcome_evidence(self, current, state="admission/error"):
        self.add_process(current, "baseline" if state == "baseline" else "unchanged")
        e = current["evidence"]
        previous = e["process"].get("comparison_at")
        items = []
        for target in e["items"]:
            if target["job"] not in ("schedd", "schedd-compute"):
                continue
            item = {k: copy.deepcopy(target[k]) for k in ("job", "node", "target_id", "scrape_observed_at", "process")}
            item["signals"] = []
            for i, d in enumerate(cron_outcome_evidence.DIMENSIONS):
                value = {**d, "state": "baseline", "current": {"value": 100, "observed_at": e["generated_at"]}}
                if previous:
                    value.update(state="unchanged", previous={"value": 100, "observed_at": previous}, delta=0)
                    selected = "admission/error" if state == "sparse" else state
                    if d["source"]+"/"+d["outcome"] == selected:
                        value["current"]["value"], value["delta"], value["state"] = 103, 3, "increased"
                if state == "sparse" and i == 0:
                    value = {**d, "state": "missing", "reason": "metric_missing"}
                item["signals"].append(value)
            items.append(item)
        counts, outcomes = cron_outcome_evidence.account(items)
        counts["expected"] = 29*sum(j["expected"] for j in e["jobs"] if j["job"] in ("schedd", "schedd-compute"))
        incomplete = sum(counts[k] for k in operation_evidence.BAD) > 0
        status, reason = ("baseline", "comparison_not_requested") if state == "baseline" else ("increased", "cron_outcome_increases_observed") if counts["increased"] else ("incomplete", "incomplete_cron_outcome_evidence") if incomplete else ("unchanged", "")
        h = {"scope": operation_evidence.SCOPE, "state": status, "counts": counts, "outcomes": outcomes, "incomplete": incomplete, "items": items, "omitted_items": 0}
        if reason:
            h["reason"] = reason
        if previous:
            h["comparison_at"] = previous
        current["command_cron_outcomes"] = h

    def test_cron_outcome_outcomes_survive_transport(self):
        for role in ("single-box-v2", "control-plane-v2"):
            for state in ("baseline", "unchanged", "sparse", *(d["source"]+"/"+d["outcome"] for d in cron_outcome_evidence.DIMENSIONS)):
                with self.subTest(role=role, state=state):
                    response, current = self.response(), self.build_evidence(role)
                    self.add_app_task_health(current)
                    self.add_command_cron_health(current)
                    self.add_cron_outcome_evidence(current, state)
                    response["runtime_builds"] = current
                    self.build_response_file(response, role)
                    result, saved = self.invoke()
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertEqual(saved["runtime_builds"], current)
                    self.assertEqual(saved["checks"], response["checks"])
                    summary = (self.root/"summary").read_text()
                    self.assertIn("Command-cron outcomes (advisory)", summary)
                    self.assertNotIn("operator-only-node", summary)
                    self.assertNotIn("103", summary)
                    h = saved["runtime_builds"]["command_cron_outcomes"]
                    if state == "baseline":
                        self.assertEqual(h["counts"]["increased"], 0)
                    if state == "sparse":
                        self.assertGreater(h["counts"]["missing"], 0)
                        self.assertGreater(h["counts"]["increased"], 0)

    def test_invalid_cron_outcome_evidence_is_isolated(self):
        for fault in ("missing_scope", "wrong_source", "missing_value", "null_value", "missing_delta", "forged_delta", "clock_mismatch", "uncertain_window", "counter_reset", "bad_summary", "wrong_window", "unknown_field"):
            with self.subTest(fault=fault):
                response, current = self.response(), self.build_evidence("single-box-v2")
                self.add_app_task_health(current)
                self.add_cron_outcome_evidence(current)
                h = current["command_cron_outcomes"]
                point = h["items"][0]["signals"][26]
                if fault == "missing_scope":
                    h.pop("scope")
                elif fault == "wrong_source":
                    point["source"] = "arbitrary"
                elif fault == "missing_value":
                    point["current"].pop("value")
                elif fault == "null_value":
                    point["current"]["value"] = None
                elif fault == "missing_delta":
                    point.pop("delta")
                elif fault == "forged_delta":
                    point["delta"] = 900
                elif fault == "clock_mismatch":
                    point["current"]["observed_at"] = (dt.datetime.fromisoformat(point["current"]["observed_at"].replace("Z", "+00:00"))-dt.timedelta(seconds=1)).isoformat()
                elif fault == "uncertain_window":
                    h["items"][0]["process"]["uptime_seconds"] = 110
                elif fault == "counter_reset":
                    point["previous"]["value"] = 104
                elif fault == "bad_summary":
                    h["outcomes"][26]["increased"] = 0
                elif fault == "wrong_window":
                    h["comparison_at"] = current["evidence"]["generated_at"]
                else:
                    point["private_error"] = "private-provider-error"
                response["runtime_builds"] = current
                self.build_response_file(response, "single-box-v2")
                result, saved = self.invoke()
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(saved["runtime_builds"]["app_task_health"], current["app_task_health"])
                self.assertEqual(saved["runtime_builds"]["evidence"], current["evidence"])
                if fault == "unknown_field":
                    self.assertIn("command_cron_outcomes", saved["runtime_builds"])
                else:
                    self.assertNotIn("command_cron_outcomes", saved["runtime_builds"])
                    self.assertEqual(saved["runtime_builds"]["command_cron_outcomes_unavailable_reason"], "invalid_cron_outcome_evidence")

    def test_cron_outcome_preview_yields_before_other_evidence(self):
        response, current = self.response(), self.build_evidence("single-box-v2")
        self.add_cron_outcome_evidence(current)
        response["runtime_builds"] = current
        response["padding"] = "x"*((1 << 20)-len(json.dumps(response, indent=2).encode())-1+300)
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(report["runtime_builds"]["command_cron_outcomes"]["counts"], current["command_cron_outcomes"]["counts"])
        self.assertEqual(report["runtime_builds"]["command_cron_outcomes_envelope_omitted_items"], 1)
        self.assertEqual(report["runtime_builds"]["evidence"], current["evidence"])

    def test_cron_outcome_history_keeps_error_after_gap_and_clipping(self):
        response = self.history_response(states=("matching", "unknown", "matching"), ages=(3, 2, 1), role="single-box-v2")
        current = response["runtime_builds"]
        self.add_cron_outcome_evidence(current, "unchanged")
        progress = current["progress"]
        first, last = progress["first_observed_at"], progress["last_observed_at"]
        gap = (dt.datetime.fromisoformat(last.replace("Z", "+00:00"))-dt.timedelta(seconds=1)).isoformat().replace("+00:00", "Z")
        h = current["command_cron_outcomes"]
        events = [{"at": gap, "observation": 2, "state": "unknown", "reason": "source_server_error", "source_http_status": 503},
                  {"at": last, "observation": 3, "state": "matching", "evidence_generated_at": current["evidence"]["generated_at"], "counts": current["evidence"]["counts"], "jobs": current["evidence"]["jobs"], "process": current["evidence"]["process"],
                   "command_cron_outcomes": {k: h[k] for k in ("scope", "state", "reason", "counts", "outcomes", "incomplete") if k in h}}]
        p = dict.fromkeys(cron_outcome_evidence.BOOLS, False)
        p.update(observed_increases=True, observed_outcomes=[{"source":"admission","outcome":"error"}], observed_incomplete_evidence=True, first_increase_at=first, last_increase_at=first)
        progress["command_cron_outcomes"] = p
        current["history"].update(events=events, retained_events=2, total_events=3, dropped_events=1, truncated=True, retained_bytes=builds.build_history_bytes(events))
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(report["runtime_builds"]["history"], current["history"])
        self.assertEqual(report["runtime_builds"]["progress"], current["progress"])
        p["observed_increases"] = False
        self.build_response_file(response, "single-box-v2")
        _, report = self.invoke()
        self.assertEqual(report["runtime_builds"]["progress"]["command_cron_outcomes_unavailable_reason"], "invalid_history")
        self.assertEqual(report["runtime_builds"]["history"], current["history"])

    def add_command_cron_health(self, current, state="empty"):
        e = current["evidence"]
        targets = [item for item in e["items"] if item["job"] in command_cron_evidence.jobs_for_role(e["expected_role"])]
        target = targets[0]
        now = dt.datetime.fromisoformat(e["generated_at"].replace("Z", "+00:00"))
        component = lambda v: {"state": "available", "observed_at": e["generated_at"], "values": v}
        counts = dict.fromkeys(command_cron_evidence.COUNTS, 0)
        counts.update(expected=1, observed=1, complete=1, empty_scopes=1, enabled_scopes=1, sweep_idle_scopes=1)
        classes = [{"class": p, "nonempty_scopes": 0, "empty_scopes": 1, "unknown_age_scopes": 0} for p in command_cron_evidence.CLASSES]
        values = [{"class": p, "state": "empty", "observed_at": e["generated_at"], "values": {"schedules": 0, "unknown_age": 0}} for p in command_cron_evidence.CLASSES]
        item = {"node": target["node"], "target_id": target["target_id"], "scrape_observed_at": e["generated_at"], "state": "available",
                "collection": component({"success": True, "last_success_at": e["generated_at"]}),
                "observation": component({"enabled": True}), "classes": values,
                "sweep": component({"running": False, "last_started_at": (now-dt.timedelta(seconds=20)).isoformat().replace("+00:00", "Z"), "last_success_at": (now-dt.timedelta(seconds=10)).isoformat().replace("+00:00", "Z")})}
        h = {"scope": command_cron_evidence.SCOPE, "state": "empty", "counts": counts, "classes": classes, "incomplete": False, "items": [item], "omitted_items": 0}
        if state in ("nonempty", "unknown_ages", "partial", "observer_disabled", "inactive"):
            h.update(state="nonempty", reason="command_cron_evidence_observed")
            counts.update(nonempty_scopes=1, empty_scopes=0, due_scopes=1)
            classes[0].update(nonempty_scopes=1, empty_scopes=0)
            values[0].update(state="nonempty", values={"schedules": 100000, "unknown_age": 1,
                             "oldest_due_at": (now-dt.timedelta(seconds=3600)).isoformat().replace("+00:00", "Z"), "oldest_known_due_age_seconds": 3600})
            classes[0]["unknown_age_scopes"] = counts["unknown_age_scopes"] = 1
            if state == "unknown_ages":
                values[0]["values"] = {"schedules": 100000, "unknown_age": 100000}
        if state == "partial":
            h["incomplete"], item["state"] = True, "missing"
            counts.update(complete=0, missing=1)
            classes[2]["empty_scopes"] = 0
            values[2].clear()
            values[2].update({"class": "replacement_wait", "state": "missing", "reason": "metric_missing"})
        elif state == "observer_disabled":
            item["observation"]["values"]["enabled"] = False
            counts.update(enabled_scopes=0, disabled_scopes=1)
        elif state in ("collection_failed", "never_observed", "stale_collection"):
            h.update(state="incomplete", reason="incomplete_command_cron_evidence", incomplete=True)
            item["state"] = "stale" if state == "stale_collection" else "unavailable"
            counts.update(complete=0, empty_scopes=0)
            counts[item["state"]] = 1
            collection = item["collection"]["values"]
            reason = "collection_stale" if state == "stale_collection" else "collection_failed"
            if state == "stale_collection":
                collection["last_success_at"] = (now-dt.timedelta(seconds=180)).isoformat().replace("+00:00", "Z")
            else:
                collection["success"] = False
                counts["unsuccessful_collections"] = 1
                if state == "never_observed":
                    collection.pop("last_success_at")
                    reason = "collection_not_observed"
            for cohort, value in zip(classes, values):
                cohort["empty_scopes"] = 0
                value.clear()
                value.update({"class": cohort["class"], "state": item["state"], "reason": reason})
        if state == "inactive":
            counts.update(due_scopes=0, inactive_scopes=1, unknown_age_scopes=1)
            classes[0].update(nonempty_scopes=0, empty_scopes=1, unknown_age_scopes=0)
            classes[5].update(nonempty_scopes=1, empty_scopes=0, unknown_age_scopes=1)
            values[0].update(state="empty", values={"schedules": 0, "unknown_age": 0})
            values[5].update(state="nonempty", values={"schedules": 100000, "unknown_age": 100000})
        if state in ("sweep_running", "sweep_not_started", "sweep_never_succeeded"):
            v = item["sweep"]["values"]
            if state == "sweep_running":
                v.update(running=True, last_started_at=e["generated_at"])
                counts.update(sweep_idle_scopes=0, sweep_running_scopes=1)
            elif state == "sweep_not_started":
                v.pop("last_started_at")
                v.pop("last_success_at")
                counts.update(sweep_never_started_scopes=1, sweep_never_succeeded_scopes=1)
            else:
                v.pop("last_success_at")
                counts.update(sweep_never_succeeded_scopes=1)
        h["items"] = []
        for target in targets:
            clone = copy.deepcopy(item)
            clone.update(job=target["job"], target_id=target["target_id"], node=target["node"])
            h["items"].append(clone)
        for key in counts:
            counts[key] *= len(targets)
        counts["expected"] = sum(j["expected"] for j in e["jobs"] if j["job"] in command_cron_evidence.jobs_for_role(e["expected_role"]))
        for cls in classes:
            for key in command_cron_evidence.CLASS_COUNTS:
                cls[key] *= len(targets)
        current["command_cron_health"] = h

    def test_command_cron_classes_survive_transport(self):
        for role in ("single-box-v2", "control-plane-v2"):
            for state in ("empty", "nonempty", "unknown_ages", "partial", "observer_disabled", "inactive", "sweep_running", "sweep_not_started", "sweep_never_succeeded", "collection_failed", "never_observed", "stale_collection"):
                with self.subTest(role=role, state=state):
                    response, current = self.response(), self.build_evidence(role)
                    self.add_handoff_health(current)
                    self.add_command_cron_health(current, state)
                    response["runtime_builds"] = current
                    self.build_response_file(response, role)
                    result, report = self.invoke()
                    self.assertEqual(result.returncode, 0)
                    self.assertEqual(report["checks"], response["checks"])
                    self.assertEqual(report["runtime_builds"], current)
                    summary = (self.root/"summary").read_text()
                    self.assertIn("Command-cron admission (advisory)", summary)
                    self.assertNotIn("operator-only-node", summary)
                    self.assertNotIn("schedules=100000", summary)
                    if state == "unknown_ages":
                        self.assertNotIn("oldest_known_due_age_seconds", report["runtime_builds"]["command_cron_health"]["items"][0]["classes"][0]["values"])

    def test_invalid_command_cron_is_isolated(self):
        for fault in ("missing_success", "missing_enabled", "missing_unknown", "missing_age", "null_schedules", "bad_clock", "snapshot_clocks", "class_order", "bad_counts", "missing_scope", "wrong_scope", "missing_running", "bad_sweep_clock", "bad_sweep_pair", "bad_due_scopes", "unknown_field"):
            response, current = self.response(), self.build_evidence("single-box-v2")
            self.add_handoff_health(current)
            self.add_command_cron_health(current, "nonempty")
            h = current["command_cron_health"]
            item, cohort = h["items"][0], h["items"][0]["classes"][0]
            if fault == "missing_running":
                item["sweep"]["values"].pop("running")
            elif fault == "bad_sweep_clock":
                item["sweep"]["values"]["last_started_at"] = "bad-clock"
            elif fault == "bad_sweep_pair":
                item["sweep"]["values"]["running"] = True
            elif fault == "bad_due_scopes":
                h["counts"]["due_scopes"] = 0
            elif fault == "missing_scope":
                h.pop("scope")
            elif fault == "wrong_scope":
                h["scope"] = "unique_deployments"
            elif fault == "missing_success":
                del item["collection"]["values"]["success"]
            elif fault == "missing_enabled":
                del item["observation"]["values"]["enabled"]
            elif fault == "missing_unknown":
                del cohort["values"]["unknown_age"]
            elif fault == "missing_age":
                del cohort["values"]["oldest_known_due_age_seconds"]
            elif fault == "null_schedules":
                cohort["values"]["schedules"] = None
            elif fault == "bad_clock":
                cohort["values"]["oldest_due_at"] = "not-a-clock"
            elif fault == "snapshot_clocks":
                cohort["observed_at"] = (dt.datetime.fromisoformat(cohort["observed_at"].replace("Z", "+00:00"))-dt.timedelta(seconds=1)).isoformat()
            elif fault == "class_order":
                cohort["class"] = "replacement_wait"
            elif fault == "bad_counts":
                h["classes"][0]["unknown_age_scopes"] = 0
            else:
                item["private_error"] = "private-provider-error"
            response["runtime_builds"] = current
            self.build_response_file(response, "single-box-v2")
            result, report = self.invoke()
            self.assertEqual(result.returncode, 0)
            saved = report["runtime_builds"]
            self.assertEqual(saved["handoff_health"], current["handoff_health"])
            self.assertEqual(saved["evidence"], current["evidence"])
            if fault == "unknown_field":
                self.assertIn("command_cron_health", saved)
                self.assertNotIn("private-provider-error", json.dumps(saved))
            else:
                self.assertNotIn("command_cron_health", saved)
                self.assertEqual(saved["command_cron_health_unavailable_reason"], "invalid_command_cron_evidence")

    def test_command_cron_history_preserves_gaps_and_unknown_ages(self):
        response = self.history_response(states=("matching", "matching", "unknown", "matching"), ages=(4, 3, 2, 1), role="single-box-v2")
        current = response["runtime_builds"]
        first = dt.datetime.fromisoformat(current["progress"]["first_observed_at"].replace("Z", "+00:00"))
        clocks = [(first+dt.timedelta(seconds=i)).isoformat().replace("+00:00", "Z") for i in range(4)]
        events = []
        for i, state in enumerate(("empty", "unknown_ages", "gap", "empty")):
            if state == "gap":
                events.append({"at": clocks[i], "observation": i+1, "state": "unknown", "reason": "source_server_error", "source_http_status": 503})
                continue
            value = self.build_evidence("single-box-v2")
            value["evidence"]["generated_at"] = clocks[i]
            self.add_command_cron_health(value, state)
            h = value["command_cron_health"]
            events.append({"at": clocks[i], "observation": i+1, "state": "matching", "evidence_generated_at": clocks[i], "counts": value["evidence"]["counts"], "jobs": value["evidence"]["jobs"], "command_cron_health": {k: h[k] for k in ("scope", "state", "reason", "counts", "classes", "incomplete") if k in h}})
            if i == 3:
                current["command_cron_health"] = h
        p = {"nonempty_transitions": 1, "empty_throughout_observed_polls": False, "observed_incomplete_evidence": True, "observed_unknown_ages": True,
             "first_nonempty_at": clocks[1], "last_nonempty_at": clocks[1], "first_empty_at": clocks[0], "last_empty_at": clocks[3]}
        current["progress"]["command_cron_health"] = p
        current["history"].update(events=events, total_events=4, retained_events=4, retained_bytes=builds.build_history_bytes(events))
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0)
        self.assertEqual(report["runtime_builds"]["history"], current["history"])
        self.assertEqual(report["runtime_builds"]["progress"], current["progress"])
        p["waiting_transitions"] = p.pop("nonempty_transitions")
        self.build_response_file(response, "single-box-v2")
        _, report = self.invoke()
        self.assertEqual(report["runtime_builds"]["progress"]["command_cron_health_unavailable_reason"], "invalid_history")
        p["nonempty_transitions"] = p.pop("waiting_transitions")
        p["observed_unknown_ages"] = False
        self.build_response_file(response, "single-box-v2")
        _, report = self.invoke()
        self.assertEqual(report["runtime_builds"]["progress"]["command_cron_health_unavailable_reason"], "invalid_history")
        self.assertEqual(report["runtime_builds"]["history"], current["history"])

    def test_command_cron_scope_overlap_and_clipping(self):
        response, current = self.response(), self.build_evidence("single-box-v2")
        self.add_command_cron_health(current, "unknown_ages")
        h = current["command_cron_health"]
        h["items"].append(copy.deepcopy(h["items"][0]))
        h["items"][1]["target_id"] = "f"*64
        for k in ("observed", "complete", "nonempty_scopes", "unknown_age_scopes", "enabled_scopes", "due_scopes", "sweep_idle_scopes"):
            h["counts"][k] = 2
        for p in h["classes"]:
            for k in command_cron_evidence.CLASS_COUNTS:
                p[k] *= 2
        response["runtime_builds"] = current
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0)
        self.assertEqual(report["runtime_builds"]["command_cron_health"], h)
        self.assertNotIn("schedules", h["counts"])
        response["padding"] = "x"*((1 << 20)-len(json.dumps(response, indent=2).encode())-1+300)
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0)
        self.assertEqual(report["runtime_builds"]["command_cron_health"]["classes"], h["classes"])
        self.assertEqual(report["runtime_builds"]["command_cron_health"]["counts"], h["counts"])
        self.assertGreater(report["runtime_builds"]["command_cron_envelope_omitted_items"], 0)
        self.assertLessEqual(self.report.stat().st_size, 1 << 20)

    def test_command_cron_unknown_class_requires_unknown_timing(self):
        current = self.build_evidence("single-box-v2")
        self.add_command_cron_health(current, "nonempty")
        h = current["command_cron_health"]
        item = h["items"][0]
        item["classes"][0]["values"], item["classes"][9]["values"] = item["classes"][9]["values"], item["classes"][0]["values"]
        item["classes"][0]["state"], item["classes"][9]["state"] = "empty", "nonempty"
        for key in command_cron_evidence.CLASS_COUNTS:
            h["classes"][0][key], h["classes"][9][key] = h["classes"][9][key], h["classes"][0][key]
        h["counts"]["due_scopes"] = 0
        with self.assertRaises(ValueError):
            command_cron_evidence.project(h, current["evidence"])
        item["classes"][9]["values"] = {"schedules": 100000, "unknown_age": 100000}
        self.assertEqual(command_cron_evidence.project(h, current["evidence"]), h)
        item["job"] = "imaged"
        with self.assertRaises(ValueError):
            command_cron_evidence.project(h, current["evidence"])

    def add_post_capture_health(self, current, state="empty"):
        e = current["evidence"]
        targets = [item for item in e["items"] if item["job"] in post_capture_evidence.jobs_for_role(e["expected_role"])]
        target = targets[0]
        now = dt.datetime.fromisoformat(e["generated_at"].replace("Z", "+00:00"))
        component = lambda v: {"state": "available", "observed_at": e["generated_at"], "values": v}
        counts = dict.fromkeys(post_capture_evidence.COUNTS, 0)
        counts.update(expected=1, observed=1, complete=1, empty_views=1, enabled_views=1)
        classes = [{"class": p, "nonempty_views": 0, "empty_views": 1, "unknown_age_views": 0} for p in post_capture_evidence.CLASSES]
        values = [{"class": p, "state": "empty", "observed_at": e["generated_at"], "values": {"pending": 0, "unknown_age": 0}} for p in post_capture_evidence.CLASSES]
        item = {"node": target["node"], "target_id": target["target_id"], "scrape_observed_at": e["generated_at"], "state": "available",
                "collection": component({"success": True, "last_success_at": e["generated_at"]}),
                "observation": component({"enabled": True}), "classes": values}
        h = {"scope": post_capture_evidence.SCOPE, "state": "empty", "counts": counts, "classes": classes, "incomplete": False, "items": [item], "omitted_items": 0}
        if state in ("nonempty", "unknown_ages", "partial", "disabled"):
            h.update(state="nonempty", reason="post_capture_evidence_observed")
            counts.update(nonempty_views=1, empty_views=0)
            classes[0].update(nonempty_views=1, empty_views=0)
            values[0].update(state="nonempty", values={"pending": 100000, "unknown_age": 1,
                             "oldest_boundary_at": (now-dt.timedelta(seconds=3600)).isoformat().replace("+00:00", "Z"), "oldest_known_age_seconds": 3600})
            classes[0]["unknown_age_views"] = counts["unknown_age_views"] = 1
            if state == "unknown_ages":
                values[0]["values"] = {"pending": 100000, "unknown_age": 100000}
        if state == "partial":
            h["incomplete"], item["state"] = True, "missing"
            counts.update(complete=0, missing=1)
            classes[2]["empty_views"] = 0
            values[2].clear()
            values[2].update({"class": "dependency_held", "state": "missing", "reason": "metric_missing"})
        elif state == "disabled":
            item["observation"]["values"]["enabled"] = False
            counts.update(enabled_views=0, disabled_views=1)
        elif state in ("collection_failed", "never_observed", "stale_collection"):
            h.update(state="incomplete", reason="incomplete_post_capture_evidence", incomplete=True)
            item["state"] = "stale" if state == "stale_collection" else "unavailable"
            counts.update(complete=0, empty_views=0)
            counts[item["state"]] = 1
            collection = item["collection"]["values"]
            reason = "collection_stale" if state == "stale_collection" else "collection_failed"
            if state == "stale_collection":
                collection["last_success_at"] = (now-dt.timedelta(seconds=180)).isoformat().replace("+00:00", "Z")
            else:
                collection["success"] = False
                counts["unsuccessful_collections"] = 1
                if state == "never_observed":
                    collection.pop("last_success_at")
                    reason = "collection_not_observed"
            for cohort, value in zip(classes, values):
                cohort["empty_views"] = 0
                value.clear()
                value.update({"class": cohort["class"], "state": item["state"], "reason": reason})
        h["items"] = []
        for target in targets:
            clone = copy.deepcopy(item)
            clone.update(job=target["job"], target_id=target["target_id"], node=target["node"])
            h["items"].append(clone)
        for key in counts:
            counts[key] *= len(targets)
        counts["expected"] = sum(j["expected"] for j in e["jobs"] if j["job"] in post_capture_evidence.jobs_for_role(e["expected_role"]))
        for cls in classes:
            for key in post_capture_evidence.CLASS_COUNTS:
                cls[key] *= len(targets)
        current["post_capture_health"] = h

    def test_post_capture_classes_survive_transport(self):
        for role in ("single-box-v2", "control-plane-v2"):
            for state in ("empty", "nonempty", "unknown_ages", "partial", "disabled", "collection_failed", "never_observed", "stale_collection"):
                with self.subTest(role=role, state=state):
                    response, current = self.response(), self.build_evidence(role)
                    self.add_handoff_health(current)
                    self.add_post_capture_health(current, state)
                    response["runtime_builds"] = current
                    self.build_response_file(response, role)
                    result, report = self.invoke()
                    self.assertEqual(result.returncode, 0)
                    self.assertEqual(report["checks"], response["checks"])
                    self.assertEqual(report["runtime_builds"], current)
                    summary = (self.root/"summary").read_text()
                    self.assertIn("Post-capture activation (advisory)", summary)
                    self.assertNotIn("operator-only-node", summary)
                    self.assertNotIn("pending=100000", summary)
                    if state == "unknown_ages":
                        self.assertNotIn("oldest_known_age_seconds", report["runtime_builds"]["post_capture_health"]["items"][0]["classes"][0]["values"])

    def test_invalid_post_capture_is_isolated(self):
        for fault in ("missing_success", "missing_enabled", "missing_unknown", "missing_age", "null_pending", "bad_clock", "snapshot_clocks", "class_order", "bad_counts", "missing_scope", "wrong_scope", "unknown_field"):
            response, current = self.response(), self.build_evidence("single-box-v2")
            self.add_handoff_health(current)
            self.add_post_capture_health(current, "nonempty")
            h = current["post_capture_health"]
            item, cohort = h["items"][0], h["items"][0]["classes"][0]
            if fault == "missing_scope":
                h.pop("scope")
            elif fault == "wrong_scope":
                h["scope"] = "unique_deployments"
            elif fault == "missing_success":
                del item["collection"]["values"]["success"]
            elif fault == "missing_enabled":
                del item["observation"]["values"]["enabled"]
            elif fault == "missing_unknown":
                del cohort["values"]["unknown_age"]
            elif fault == "missing_age":
                del cohort["values"]["oldest_known_age_seconds"]
            elif fault == "null_pending":
                cohort["values"]["pending"] = None
            elif fault == "bad_clock":
                cohort["values"]["oldest_boundary_at"] = "not-a-clock"
            elif fault == "snapshot_clocks":
                cohort["observed_at"] = (dt.datetime.fromisoformat(cohort["observed_at"].replace("Z", "+00:00"))-dt.timedelta(seconds=1)).isoformat()
            elif fault == "class_order":
                cohort["class"] = "dependency_held"
            elif fault == "bad_counts":
                h["classes"][0]["unknown_age_views"] = 0
            else:
                item["private_error"] = "private-provider-error"
            response["runtime_builds"] = current
            self.build_response_file(response, "single-box-v2")
            result, report = self.invoke()
            self.assertEqual(result.returncode, 0)
            saved = report["runtime_builds"]
            self.assertEqual(saved["handoff_health"], current["handoff_health"])
            self.assertEqual(saved["evidence"], current["evidence"])
            if fault == "unknown_field":
                self.assertIn("post_capture_health", saved)
                self.assertNotIn("private-provider-error", json.dumps(saved))
            else:
                self.assertNotIn("post_capture_health", saved)
                self.assertEqual(saved["post_capture_health_unavailable_reason"], "invalid_post_capture_evidence")

    def test_post_capture_history_preserves_gaps_and_unknown_ages(self):
        response = self.history_response(states=("matching", "matching", "unknown", "matching"), ages=(4, 3, 2, 1), role="single-box-v2")
        current = response["runtime_builds"]
        first = dt.datetime.fromisoformat(current["progress"]["first_observed_at"].replace("Z", "+00:00"))
        clocks = [(first+dt.timedelta(seconds=i)).isoformat().replace("+00:00", "Z") for i in range(4)]
        events = []
        for i, state in enumerate(("empty", "unknown_ages", "gap", "empty")):
            if state == "gap":
                events.append({"at": clocks[i], "observation": i+1, "state": "unknown", "reason": "source_server_error", "source_http_status": 503})
                continue
            value = self.build_evidence("single-box-v2")
            value["evidence"]["generated_at"] = clocks[i]
            self.add_post_capture_health(value, state)
            h = value["post_capture_health"]
            events.append({"at": clocks[i], "observation": i+1, "state": "matching", "evidence_generated_at": clocks[i], "counts": value["evidence"]["counts"], "jobs": value["evidence"]["jobs"], "post_capture_health": {k: h[k] for k in ("scope", "state", "reason", "counts", "classes", "incomplete") if k in h}})
            if i == 3:
                current["post_capture_health"] = h
        p = {"nonempty_transitions": 1, "empty_throughout_observed_polls": False, "observed_incomplete_evidence": True, "observed_unknown_ages": True,
             "first_nonempty_at": clocks[1], "last_nonempty_at": clocks[1], "first_empty_at": clocks[0], "last_empty_at": clocks[3]}
        current["progress"]["post_capture_health"] = p
        current["history"].update(events=events, total_events=4, retained_events=4, retained_bytes=builds.build_history_bytes(events))
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0)
        self.assertEqual(report["runtime_builds"]["history"], current["history"])
        self.assertEqual(report["runtime_builds"]["progress"], current["progress"])
        p["waiting_transitions"] = p.pop("nonempty_transitions")
        self.build_response_file(response, "single-box-v2")
        _, report = self.invoke()
        self.assertEqual(report["runtime_builds"]["progress"]["post_capture_health_unavailable_reason"], "invalid_history")
        p["nonempty_transitions"] = p.pop("waiting_transitions")
        p["observed_unknown_ages"] = False
        self.build_response_file(response, "single-box-v2")
        _, report = self.invoke()
        self.assertEqual(report["runtime_builds"]["progress"]["post_capture_health_unavailable_reason"], "invalid_history")
        self.assertEqual(report["runtime_builds"]["history"], current["history"])

    def test_post_capture_scope_overlap_and_clipping(self):
        response, current = self.response(), self.build_evidence("single-box-v2")
        self.add_post_capture_health(current, "unknown_ages")
        h = current["post_capture_health"]
        h["items"].append(copy.deepcopy(h["items"][0]))
        h["items"][1]["target_id"] = "f"*64
        for k in ("observed", "complete", "nonempty_views", "unknown_age_views", "enabled_views"):
            h["counts"][k] = 2
        for p in h["classes"]:
            for k in post_capture_evidence.CLASS_COUNTS:
                p[k] *= 2
        response["runtime_builds"] = current
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0)
        self.assertEqual(report["runtime_builds"]["post_capture_health"], h)
        self.assertNotIn("pending", h["counts"])
        response["padding"] = "x"*((1 << 20)-len(json.dumps(response, indent=2).encode())-1+300)
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0)
        self.assertEqual(report["runtime_builds"]["post_capture_health"]["classes"], h["classes"])
        self.assertEqual(report["runtime_builds"]["post_capture_health"]["counts"], h["counts"])
        self.assertGreater(report["runtime_builds"]["post_capture_envelope_omitted_items"], 0)
        self.assertLessEqual(self.report.stat().st_size, 1 << 20)

    def test_post_capture_unknown_class_requires_unknown_timing(self):
        current = self.build_evidence("single-box-v2")
        self.add_post_capture_health(current, "nonempty")
        h = current["post_capture_health"]
        item = h["items"][0]
        item["classes"][0]["values"], item["classes"][7]["values"] = item["classes"][7]["values"], item["classes"][0]["values"]
        item["classes"][0]["state"], item["classes"][7]["state"] = "empty", "nonempty"
        for key in post_capture_evidence.CLASS_COUNTS:
            h["classes"][0][key], h["classes"][7][key] = h["classes"][7][key], h["classes"][0][key]
        with self.assertRaises(ValueError):
            post_capture_evidence.project(h, current["evidence"])
        item["classes"][7]["values"] = {"pending": 100000, "unknown_age": 100000}
        self.assertEqual(post_capture_evidence.project(h, current["evidence"]), h)
        item["job"] = "schedd"
        with self.assertRaises(ValueError):
            post_capture_evidence.project(h, current["evidence"])

    def add_snapshot_prime_health(self, current, state="empty"):
        e = current["evidence"]
        targets = [item for item in e["items"] if item["job"] in snapshot_prime_evidence.jobs_for_role(e["expected_role"])]
        target = targets[0]
        now = dt.datetime.fromisoformat(e["generated_at"].replace("Z", "+00:00"))
        component = lambda v: {"state": "available", "observed_at": e["generated_at"], "values": v}
        counts = dict.fromkeys(snapshot_prime_evidence.COUNTS, 0)
        counts.update(expected=1, observed=1, complete=1, empty_scopes=1, enabled_scopes=1)
        classes = [{"class": p, "nonempty_scopes": 0, "empty_scopes": 1, "unknown_age_scopes": 0} for p in snapshot_prime_evidence.CLASSES]
        values = [{"class": p, "state": "empty", "observed_at": e["generated_at"], "values": {"pending": 0, "unknown_age": 0}} for p in snapshot_prime_evidence.CLASSES]
        item = {"node": target["node"], "target_id": target["target_id"], "scrape_observed_at": e["generated_at"], "state": "available",
                "collection": component({"success": True, "last_success_at": e["generated_at"]}),
                "observation": component({"enabled": True}), "classes": values}
        h = {"state": "empty", "counts": counts, "classes": classes, "incomplete": False, "items": [item], "omitted_items": 0}
        if state in ("nonempty", "unknown_ages", "partial", "disabled"):
            h.update(state="nonempty", reason="snapshot_attempts_observed")
            counts.update(nonempty_scopes=1, empty_scopes=0)
            classes[0].update(nonempty_scopes=1, empty_scopes=0)
            values[0].update(state="nonempty", values={"pending": 100000, "unknown_age": 1,
                             "oldest_attempt_at": (now-dt.timedelta(seconds=3600)).isoformat().replace("+00:00", "Z"), "oldest_known_age_seconds": 3600})
            classes[0]["unknown_age_scopes"] = counts["unknown_age_scopes"] = 1
            if state == "unknown_ages":
                values[0]["values"] = {"pending": 100000, "unknown_age": 100000}
        if state == "partial":
            h["incomplete"], item["state"] = True, "missing"
            counts.update(complete=0, missing=1)
            classes[2]["empty_scopes"] = 0
            values[2].clear()
            values[2].update({"class": "active", "state": "missing", "reason": "metric_missing"})
        elif state == "disabled":
            item["observation"]["values"]["enabled"] = False
            counts.update(enabled_scopes=0, disabled_scopes=1)
        elif state in ("collection_failed", "never_observed", "stale_collection"):
            h.update(state="incomplete", reason="incomplete_snapshot_prime_evidence", incomplete=True)
            item["state"] = "stale" if state == "stale_collection" else "unavailable"
            counts.update(complete=0, empty_scopes=0)
            counts[item["state"]] = 1
            collection = item["collection"]["values"]
            reason = "collection_stale" if state == "stale_collection" else "collection_failed"
            if state == "stale_collection":
                collection["last_success_at"] = (now-dt.timedelta(seconds=180)).isoformat().replace("+00:00", "Z")
            else:
                collection["success"] = False
                counts["unsuccessful_collections"] = 1
                if state == "never_observed":
                    collection.pop("last_success_at")
                    reason = "collection_not_observed"
            for cohort, value in zip(classes, values):
                cohort["empty_scopes"] = 0
                value.clear()
                value.update({"class": cohort["class"], "state": item["state"], "reason": reason})
        h["items"] = []
        for target in targets:
            clone = copy.deepcopy(item)
            clone.update(job=target["job"], target_id=target["target_id"], node=target["node"])
            h["items"].append(clone)
        for key in counts:
            counts[key] *= len(targets)
        counts["expected"] = sum(j["expected"] for j in e["jobs"] if j["job"] in snapshot_prime_evidence.jobs_for_role(e["expected_role"]))
        for cls in classes:
            for key in snapshot_prime_evidence.CLASS_COUNTS:
                cls[key] *= len(targets)
        current["snapshot_prime_health"] = h

    def test_snapshot_prime_classes_survive_transport(self):
        for role in ("single-box-v2", "control-plane-v2"):
            for state in ("empty", "nonempty", "unknown_ages", "partial", "disabled", "collection_failed", "never_observed", "stale_collection"):
                with self.subTest(role=role, state=state):
                    response, current = self.response(), self.build_evidence(role)
                    self.add_handoff_health(current)
                    self.add_snapshot_prime_health(current, state)
                    response["runtime_builds"] = current
                    self.build_response_file(response, role)
                    result, report = self.invoke()
                    self.assertEqual(result.returncode, 0)
                    self.assertEqual(report["checks"], response["checks"])
                    self.assertEqual(report["runtime_builds"], current)
                    summary = (self.root/"summary").read_text()
                    self.assertIn("Snapshot priming (advisory)", summary)
                    self.assertNotIn("operator-only-node", summary)
                    self.assertNotIn("pending=100000", summary)
                    if state == "unknown_ages":
                        self.assertNotIn("oldest_known_age_seconds", report["runtime_builds"]["snapshot_prime_health"]["items"][0]["classes"][0]["values"])

    def test_invalid_snapshot_prime_is_isolated(self):
        for fault in ("missing_success", "missing_enabled", "missing_unknown", "missing_age", "null_pending", "bad_clock", "snapshot_clocks", "class_order", "bad_counts", "unknown_field"):
            response, current = self.response(), self.build_evidence("single-box-v2")
            self.add_handoff_health(current)
            self.add_snapshot_prime_health(current, "nonempty")
            h = current["snapshot_prime_health"]
            item, cohort = h["items"][0], h["items"][0]["classes"][0]
            if fault == "missing_success":
                del item["collection"]["values"]["success"]
            elif fault == "missing_enabled":
                del item["observation"]["values"]["enabled"]
            elif fault == "missing_unknown":
                del cohort["values"]["unknown_age"]
            elif fault == "missing_age":
                del cohort["values"]["oldest_known_age_seconds"]
            elif fault == "null_pending":
                cohort["values"]["pending"] = None
            elif fault == "bad_clock":
                cohort["values"]["oldest_attempt_at"] = "not-a-clock"
            elif fault == "snapshot_clocks":
                cohort["observed_at"] = (dt.datetime.fromisoformat(cohort["observed_at"].replace("Z", "+00:00"))-dt.timedelta(seconds=1)).isoformat()
            elif fault == "class_order":
                cohort["class"] = "active"
            elif fault == "bad_counts":
                h["classes"][0]["unknown_age_scopes"] = 0
            else:
                item["private_error"] = "private-provider-error"
            response["runtime_builds"] = current
            self.build_response_file(response, "single-box-v2")
            result, report = self.invoke()
            self.assertEqual(result.returncode, 0)
            saved = report["runtime_builds"]
            self.assertEqual(saved["handoff_health"], current["handoff_health"])
            self.assertEqual(saved["evidence"], current["evidence"])
            if fault == "unknown_field":
                self.assertIn("snapshot_prime_health", saved)
                self.assertNotIn("private-provider-error", json.dumps(saved))
            else:
                self.assertNotIn("snapshot_prime_health", saved)
                self.assertEqual(saved["snapshot_prime_health_unavailable_reason"], "invalid_snapshot_prime_evidence")

    def test_snapshot_prime_history_preserves_gaps_and_unknown_ages(self):
        response = self.history_response(states=("matching", "matching", "unknown", "matching"), ages=(4, 3, 2, 1), role="single-box-v2")
        current = response["runtime_builds"]
        first = dt.datetime.fromisoformat(current["progress"]["first_observed_at"].replace("Z", "+00:00"))
        clocks = [(first+dt.timedelta(seconds=i)).isoformat().replace("+00:00", "Z") for i in range(4)]
        events = []
        for i, state in enumerate(("empty", "unknown_ages", "gap", "empty")):
            if state == "gap":
                events.append({"at": clocks[i], "observation": i+1, "state": "unknown", "reason": "source_server_error", "source_http_status": 503})
                continue
            value = self.build_evidence("single-box-v2")
            value["evidence"]["generated_at"] = clocks[i]
            self.add_snapshot_prime_health(value, state)
            h = value["snapshot_prime_health"]
            events.append({"at": clocks[i], "observation": i+1, "state": "matching", "evidence_generated_at": clocks[i], "counts": value["evidence"]["counts"], "jobs": value["evidence"]["jobs"], "snapshot_prime_health": {k: h[k] for k in ("state", "reason", "counts", "classes", "incomplete") if k in h}})
            if i == 3:
                current["snapshot_prime_health"] = h
        p = {"nonempty_transitions": 1, "empty_throughout_observed_polls": False, "observed_incomplete_evidence": True, "observed_unknown_ages": True,
             "first_nonempty_at": clocks[1], "last_nonempty_at": clocks[1], "first_empty_at": clocks[0], "last_empty_at": clocks[3]}
        current["progress"]["snapshot_prime_health"] = p
        current["history"].update(events=events, total_events=4, retained_events=4, retained_bytes=builds.build_history_bytes(events))
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0)
        self.assertEqual(report["runtime_builds"]["history"], current["history"])
        self.assertEqual(report["runtime_builds"]["progress"], current["progress"])
        p["waiting_transitions"] = p.pop("nonempty_transitions")
        self.build_response_file(response, "single-box-v2")
        _, report = self.invoke()
        self.assertEqual(report["runtime_builds"]["progress"]["snapshot_prime_health_unavailable_reason"], "invalid_history")
        p["nonempty_transitions"] = p.pop("waiting_transitions")
        p["observed_unknown_ages"] = False
        self.build_response_file(response, "single-box-v2")
        _, report = self.invoke()
        self.assertEqual(report["runtime_builds"]["progress"]["snapshot_prime_health_unavailable_reason"], "invalid_history")
        self.assertEqual(report["runtime_builds"]["history"], current["history"])

    def test_snapshot_prime_scope_overlap_and_clipping(self):
        response, current = self.response(), self.build_evidence("single-box-v2")
        self.add_snapshot_prime_health(current, "unknown_ages")
        h = current["snapshot_prime_health"]
        h["items"].append(copy.deepcopy(h["items"][0]))
        h["items"][1]["target_id"] = "f"*64
        for k in ("observed", "complete", "nonempty_scopes", "unknown_age_scopes", "enabled_scopes"):
            h["counts"][k] = 2
        for p in h["classes"]:
            for k in snapshot_prime_evidence.CLASS_COUNTS:
                p[k] *= 2
        response["runtime_builds"] = current
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0)
        self.assertEqual(report["runtime_builds"]["snapshot_prime_health"], h)
        self.assertNotIn("pending", h["counts"])
        response["padding"] = "x"*((1 << 20)-len(json.dumps(response, indent=2).encode())-1+300)
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0)
        self.assertEqual(report["runtime_builds"]["snapshot_prime_health"]["classes"], h["classes"])
        self.assertEqual(report["runtime_builds"]["snapshot_prime_health"]["counts"], h["counts"])
        self.assertGreater(report["runtime_builds"]["snapshot_prime_envelope_omitted_items"], 0)
        self.assertLessEqual(self.report.stat().st_size, 1 << 20)

    def test_snapshot_prime_unknown_class_requires_unknown_timing(self):
        current = self.build_evidence("single-box-v2")
        self.add_snapshot_prime_health(current, "nonempty")
        h = current["snapshot_prime_health"]
        item = h["items"][0]
        item["classes"][0]["values"], item["classes"][5]["values"] = item["classes"][5]["values"], item["classes"][0]["values"]
        item["classes"][0]["state"], item["classes"][5]["state"] = "empty", "nonempty"
        for key in snapshot_prime_evidence.CLASS_COUNTS:
            h["classes"][0][key], h["classes"][5][key] = h["classes"][5][key], h["classes"][0][key]
        with self.assertRaises(ValueError):
            snapshot_prime_evidence.project(h, current["evidence"])
        item["classes"][5]["values"] = {"pending": 100000, "unknown_age": 100000}
        self.assertEqual(snapshot_prime_evidence.project(h, current["evidence"]), h)
        item["job"] = "imaged"
        with self.assertRaises(ValueError):
            snapshot_prime_evidence.project(h, current["evidence"])

    def add_preparation_health(self, current, state="empty"):
        e = current["evidence"]
        target = next(item for item in e["items"] if item["job"] == "imaged")
        now = dt.datetime.fromisoformat(e["generated_at"].replace("Z", "+00:00"))
        component = lambda v: {"state": "available", "observed_at": e["generated_at"], "values": v}
        counts = dict.fromkeys(preparation_evidence.COUNTS, 0)
        counts.update(expected=1, observed=1, complete=1, empty_scopes=1, enabled_scopes=1)
        phases = [{"phase": p, "waiting_scopes": 0, "empty_scopes": 1, "unknown_age_scopes": 0} for p in preparation_evidence.PHASES]
        values = [{"phase": p, "state": "empty", "observed_at": e["generated_at"], "values": {"pending": 0, "unknown_age": 0}} for p in preparation_evidence.PHASES]
        item = {"node": target["node"], "target_id": target["target_id"], "scrape_observed_at": e["generated_at"], "state": "available",
                "collection": component({"success": True, "last_success_at": e["generated_at"]}),
                "observation": component({"enabled": True}), "phases": values}
        h = {"state": "empty", "counts": counts, "phases": phases, "incomplete": False, "items": [item], "omitted_items": 0}
        if state in ("waiting", "unknown_ages", "partial", "disabled"):
            h.update(state="waiting", reason="pending_preparations_observed")
            counts.update(waiting_scopes=1, empty_scopes=0)
            phases[0].update(waiting_scopes=1, empty_scopes=0)
            values[0].update(state="waiting", values={"pending": 100000, "unknown_age": 1,
                             "oldest_phase_started_at": (now-dt.timedelta(seconds=3600)).isoformat().replace("+00:00", "Z"), "oldest_known_age_seconds": 3600})
            phases[0]["unknown_age_scopes"] = counts["unknown_age_scopes"] = 1
            if state == "unknown_ages":
                values[0]["values"] = {"pending": 100000, "unknown_age": 100000}
        if state == "partial":
            h["incomplete"], item["state"] = True, "missing"
            counts.update(complete=0, missing=1)
            phases[2]["empty_scopes"] = 0
            values[2].clear()
            values[2].update(phase="scan_complete", state="missing", reason="metric_missing")
        elif state == "disabled":
            item["observation"]["values"]["enabled"] = False
            counts.update(enabled_scopes=0, disabled_scopes=1)
        elif state in ("collection_failed", "never_observed", "stale_collection"):
            h.update(state="incomplete", reason="incomplete_preparation_evidence", incomplete=True)
            item["state"] = "stale" if state == "stale_collection" else "unavailable"
            counts.update(complete=0, empty_scopes=0)
            counts[item["state"]] = 1
            collection = item["collection"]["values"]
            reason = "collection_stale" if state == "stale_collection" else "collection_failed"
            if state == "stale_collection":
                collection["last_success_at"] = (now-dt.timedelta(seconds=180)).isoformat().replace("+00:00", "Z")
            else:
                collection["success"] = False
                counts["unsuccessful_collections"] = 1
                if state == "never_observed":
                    collection.pop("last_success_at")
                    reason = "collection_not_observed"
            for phase, value in zip(phases, values):
                phase["empty_scopes"] = 0
                value.clear()
                value.update(phase=phase["phase"], state=item["state"], reason=reason)
        current["preparation_health"] = h

    def test_preparation_phases_survive_transport(self):
        for role in ("single-box-v2", "control-plane-v2"):
            for state in ("empty", "waiting", "unknown_ages", "partial", "disabled", "collection_failed", "never_observed", "stale_collection"):
                with self.subTest(role=role, state=state):
                    response, current = self.response(), self.build_evidence(role)
                    self.add_handoff_health(current)
                    self.add_preparation_health(current, state)
                    response["runtime_builds"] = current
                    self.build_response_file(response, role)
                    result, report = self.invoke()
                    self.assertEqual(result.returncode, 0)
                    self.assertEqual(report["checks"], response["checks"])
                    self.assertEqual(report["runtime_builds"], current)
                    summary = (self.root/"summary").read_text()
                    self.assertIn("Image preparation (advisory)", summary)
                    self.assertNotIn("operator-only-node", summary)
                    self.assertNotIn("pending=100000", summary)
                    if state == "unknown_ages":
                        self.assertNotIn("oldest_known_age_seconds", report["runtime_builds"]["preparation_health"]["items"][0]["phases"][0]["values"])

    def test_invalid_preparation_is_isolated(self):
        for fault in ("missing_success", "missing_enabled", "missing_unknown", "missing_age", "null_pending", "bad_clock", "snapshot_clocks", "phase_order", "bad_counts", "unknown_field"):
            response, current = self.response(), self.build_evidence("single-box-v2")
            self.add_handoff_health(current)
            self.add_preparation_health(current, "waiting")
            h = current["preparation_health"]
            item, phase = h["items"][0], h["items"][0]["phases"][0]
            if fault == "missing_success":
                del item["collection"]["values"]["success"]
            elif fault == "missing_enabled":
                del item["observation"]["values"]["enabled"]
            elif fault == "missing_unknown":
                del phase["values"]["unknown_age"]
            elif fault == "missing_age":
                del phase["values"]["oldest_known_age_seconds"]
            elif fault == "null_pending":
                phase["values"]["pending"] = None
            elif fault == "bad_clock":
                phase["values"]["oldest_phase_started_at"] = "not-a-clock"
            elif fault == "snapshot_clocks":
                phase["observed_at"] = (dt.datetime.fromisoformat(phase["observed_at"].replace("Z", "+00:00"))-dt.timedelta(seconds=1)).isoformat()
            elif fault == "phase_order":
                phase["phase"] = "scan_complete"
            elif fault == "bad_counts":
                h["phases"][0]["unknown_age_scopes"] = 0
            else:
                item["private_error"] = "private-provider-error"
            response["runtime_builds"] = current
            self.build_response_file(response, "single-box-v2")
            result, report = self.invoke()
            self.assertEqual(result.returncode, 0)
            saved = report["runtime_builds"]
            self.assertEqual(saved["handoff_health"], current["handoff_health"])
            self.assertEqual(saved["evidence"], current["evidence"])
            if fault == "unknown_field":
                self.assertIn("preparation_health", saved)
                self.assertNotIn("private-provider-error", json.dumps(saved))
            else:
                self.assertNotIn("preparation_health", saved)
                self.assertEqual(saved["preparation_health_unavailable_reason"], "invalid_preparation_evidence")

    def test_preparation_history_preserves_gaps_and_unknown_ages(self):
        response = self.history_response(states=("matching", "matching", "unknown", "matching"), ages=(4, 3, 2, 1), role="single-box-v2")
        current = response["runtime_builds"]
        first = dt.datetime.fromisoformat(current["progress"]["first_observed_at"].replace("Z", "+00:00"))
        clocks = [(first+dt.timedelta(seconds=i)).isoformat().replace("+00:00", "Z") for i in range(4)]
        events = []
        for i, state in enumerate(("empty", "unknown_ages", "gap", "empty")):
            if state == "gap":
                events.append({"at": clocks[i], "observation": i+1, "state": "unknown", "reason": "source_server_error", "source_http_status": 503})
                continue
            value = self.build_evidence("single-box-v2")
            value["evidence"]["generated_at"] = clocks[i]
            self.add_preparation_health(value, state)
            h = value["preparation_health"]
            events.append({"at": clocks[i], "observation": i+1, "state": "matching", "evidence_generated_at": clocks[i], "counts": value["evidence"]["counts"], "jobs": value["evidence"]["jobs"], "preparation_health": {k: h[k] for k in ("state", "reason", "counts", "phases", "incomplete") if k in h}})
            if i == 3:
                current["preparation_health"] = h
        p = {"waiting_transitions": 1, "empty_throughout_observed_polls": False, "observed_incomplete_evidence": True, "observed_unknown_ages": True,
             "first_waiting_at": clocks[1], "last_waiting_at": clocks[1], "first_empty_at": clocks[0], "last_empty_at": clocks[3]}
        current["progress"]["preparation_health"] = p
        current["history"].update(events=events, total_events=4, retained_events=4, retained_bytes=builds.build_history_bytes(events))
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0)
        self.assertEqual(report["runtime_builds"]["history"], current["history"])
        self.assertEqual(report["runtime_builds"]["progress"], current["progress"])
        p["observed_unknown_ages"] = False
        self.build_response_file(response, "single-box-v2")
        _, report = self.invoke()
        self.assertEqual(report["runtime_builds"]["progress"]["preparation_health_unavailable_reason"], "invalid_history")
        self.assertEqual(report["runtime_builds"]["history"], current["history"])

    def test_preparation_scope_overlap_and_clipping(self):
        response, current = self.response(), self.build_evidence("single-box-v2")
        self.add_preparation_health(current, "unknown_ages")
        h = current["preparation_health"]
        h["items"].append(copy.deepcopy(h["items"][0]))
        h["items"][1]["target_id"] = "f"*64
        for k in ("observed", "complete", "waiting_scopes", "unknown_age_scopes", "enabled_scopes"):
            h["counts"][k] = 2
        for p in h["phases"]:
            for k in preparation_evidence.PHASE_COUNTS:
                p[k] *= 2
        response["runtime_builds"] = current
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0)
        self.assertEqual(report["runtime_builds"]["preparation_health"], h)
        self.assertNotIn("pending", h["counts"])
        response["padding"] = "x"*((1 << 20)-len(json.dumps(response, indent=2).encode())-1+300)
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0)
        self.assertEqual(report["runtime_builds"]["preparation_health"]["phases"], h["phases"])
        self.assertEqual(report["runtime_builds"]["preparation_health"]["counts"], h["counts"])
        self.assertGreater(report["runtime_builds"]["preparation_envelope_omitted_items"], 0)
        self.assertLessEqual(self.report.stat().st_size, 1 << 20)

    def add_handoff_health(self, current, state="empty"):
        e = current["evidence"]
        target = next(item for item in e["items"] if item["job"] == "imaged")
        now = dt.datetime.fromisoformat(e["generated_at"].replace("Z", "+00:00"))
        clock = lambda seconds: (now-dt.timedelta(seconds=seconds)).isoformat().replace("+00:00", "Z")
        component = lambda value: {"state": "available", "observed_at": e["generated_at"], "values": value}
        collection = component({"success": True, "last_success_at": clock(1)})
        backlog = component({"pending": 0, "oldest_wait_seconds": 0})
        backlog["state"] = "empty"
        recovery = component({"enabled": True, "inflight": False, "last_attempt_at": clock(5), "last_completed_at": clock(4)})
        recovery["state"] = "waiting"
        item = {"node": target["node"], "target_id": target["target_id"], "scrape_observed_at": e["generated_at"], "state": "available", "collection": collection, "backlog": backlog, "recovery": recovery}
        counts = dict.fromkeys(handoff_evidence.COUNTS, 0)
        counts.update(expected=1, observed=1, complete=1, empty_scopes=1, waiting_recovery_scopes=1)
        h = {"state": "empty", "counts": counts, "incomplete": False, "items": [item], "omitted_items": 0}
        if state in ("waiting", "partial"):
            h.update(state="waiting", reason="pending_handoffs_observed")
            counts.update(waiting_scopes=1, empty_scopes=0)
            backlog.update(state="waiting", values={"pending": 100000, "oldest_finished_at": clock(600), "oldest_wait_seconds": 600})
        if state == "partial":
            h["incomplete"], item["state"] = True, "unavailable"
            counts.update(complete=0, unavailable=1, waiting_recovery_scopes=0)
            item["recovery"] = {"state": "unavailable", "reason": "invalid_value"}
        elif state in ("collection_failed", "never_observed", "stale_collection"):
            h.update(state="incomplete", reason="incomplete_handoff_evidence", incomplete=True)
            item["state"] = "stale" if state == "stale_collection" else "unavailable"
            counts.update(complete=0, empty_scopes=0)
            counts[item["state"]] = 1
            if state == "stale_collection":
                collection["values"]["last_success_at"] = clock(180)
                item["backlog"] = {"state": "stale", "reason": "collection_stale"}
            else:
                collection["values"]["success"] = False
                counts["unsuccessful_collections"] = 1
                if state == "never_observed":
                    collection["values"].pop("last_success_at")
                item["backlog"] = {"state": "unavailable", "reason": "collection_not_observed" if state == "never_observed" else "collection_failed"}
        elif state == "working":
            recovery.update(state="working", values={"enabled": True, "inflight": True, "last_attempt_at": clock(3600), "last_completed_at": clock(7200)})
            counts.update(waiting_recovery_scopes=0, working_recovery_scopes=1)
        elif state == "stopping":
            recovery.update(state="stopping", values={"enabled": False, "inflight": True, "last_attempt_at": clock(3600), "last_completed_at": clock(7200)})
            counts.update(waiting_recovery_scopes=0, stopping_recovery_scopes=1)
        elif state == "not_started":
            recovery.update(state="not_started", values={"enabled": True, "inflight": False})
            counts.update(waiting_recovery_scopes=0, not_started_recovery_scopes=1)
        current["handoff_health"] = h

    def test_handoff_snapshot_and_recovery_survive_transport(self):
        for role in ("single-box-v2", "control-plane-v2"):
            for state in ("empty", "waiting", "partial", "collection_failed", "never_observed", "stale_collection", "working", "stopping", "not_started"):
                with self.subTest(role=role, state=state):
                    response = self.response()
                    current = self.build_evidence(role)
                    self.add_readiness(current)
                    self.add_builder_health(current)
                    self.add_handoff_health(current, state)
                    response["runtime_builds"] = current
                    self.build_response_file(response, role)
                    result, report = self.invoke()
                    self.assertEqual(result.returncode, 0)
                    self.assertEqual(report["checks"], response["checks"])
                    self.assertEqual(report["runtime_builds"], current)
                    summary = (self.root/"summary").read_text()
                    self.assertIn("Build-to-image handoffs (advisory)", summary)
                    self.assertNotIn("operator-only-node", summary)
                    if state in ("waiting", "partial"):
                        self.assertIn("waiting_scopes=1", summary)
                        self.assertNotIn("pending=100000", summary)

    def test_invalid_handoff_metadata_preserves_other_evidence(self):
        for fault in ("missing_success", "null_pending", "missing_enabled", "bad_clock", "snapshot_clocks", "unknown_field", "wrong_role", "bad_counts"):
            response = self.response()
            current = self.build_evidence("single-box-v2")
            self.add_builder_health(current)
            self.add_handoff_health(current, "collection_failed" if fault == "missing_success" else "stopping" if fault == "missing_enabled" else "empty")
            h, item = current["handoff_health"], current["handoff_health"]["items"][0]
            if fault == "missing_success":
                item["collection"]["values"].pop("success")
            elif fault == "null_pending":
                item["backlog"]["values"]["pending"] = None
            elif fault == "missing_enabled":
                item["recovery"]["values"].pop("enabled")
            elif fault == "bad_clock":
                item["collection"]["values"]["last_success_at"] = (dt.datetime.now(dt.UTC)-dt.timedelta(minutes=4)).isoformat()
            elif fault == "snapshot_clocks":
                item["backlog"]["observed_at"] = (dt.datetime.fromisoformat(item["collection"]["observed_at"].replace("Z", "+00:00"))-dt.timedelta(seconds=1)).isoformat()
            elif fault == "bad_counts":
                h["counts"]["waiting_scopes"] = 1
            elif fault == "unknown_field":
                item["private_error"] = item["recovery"]["values"]["private_error"] = "private-provider-error"
            else:
                old = self.build_evidence()
                old["handoff_health"] = h
                current = old
            response["runtime_builds"] = current
            self.build_response_file(response, "single-box-v1" if fault == "wrong_role" else "single-box-v2")
            result, report = self.invoke()
            self.assertEqual(result.returncode, 0)
            saved = report["runtime_builds"]
            self.assertEqual(saved["evidence"], current["evidence"])
            if fault == "unknown_field":
                self.assertIn("handoff_health", saved)
            else:
                self.assertNotIn("handoff_health", saved)
                self.assertEqual(saved["handoff_health_unavailable_reason"], "invalid_handoff_evidence")
            self.assertNotIn("private-provider-error", json.dumps(report))

    def test_handoff_history_preserves_waiting_gaps_and_empty_observations(self):
        response = self.history_response(states=("matching", "matching", "unknown", "matching"), ages=(4, 3, 2, 1), role="single-box-v2")
        current = response["runtime_builds"]
        first = dt.datetime.fromisoformat(current["progress"]["first_observed_at"].replace("Z", "+00:00"))
        clocks = [(first+dt.timedelta(seconds=i)).isoformat().replace("+00:00", "Z") for i in range(4)]
        events = []
        for i, state in enumerate(("empty", "waiting", "gap", "empty")):
            if state == "gap":
                events.append({"at": clocks[i], "observation": i+1, "state": "unknown", "reason": "source_server_error", "source_http_status": 503})
                continue
            value = self.build_evidence("single-box-v2")
            value["evidence"]["generated_at"] = clocks[i]
            self.add_handoff_health(value, state)
            h = value["handoff_health"]
            events.append({"at": clocks[i], "observation": i+1, "state": "matching", "evidence_generated_at": clocks[i], "counts": value["evidence"]["counts"], "jobs": value["evidence"]["jobs"], "handoff_health": {key: h[key] for key in ("state", "reason", "counts", "incomplete") if key in h}})
            if i == 3:
                current["handoff_health"] = h
        p = {"waiting_transitions": 1, "empty_throughout_observed_polls": False, "observed_incomplete_evidence": True,
             "first_waiting_at": clocks[1], "last_waiting_at": clocks[1], "first_empty_at": clocks[0], "last_empty_at": clocks[3]}
        current["progress"]["handoff_health"] = p
        current["history"].update(events=events, total_events=4, retained_events=4, retained_bytes=builds.build_history_bytes(events))
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0)
        self.assertEqual(report["runtime_builds"]["history"], current["history"])
        self.assertEqual(report["runtime_builds"]["progress"], current["progress"])
        p["waiting_transitions"] = 0
        self.build_response_file(response, "single-box-v2")
        _, report = self.invoke()
        self.assertEqual(report["runtime_builds"]["progress"]["handoff_health_unavailable_reason"], "invalid_history")
        self.assertEqual(report["runtime_builds"]["history"], current["history"])

    def test_handoff_scope_overlap_and_report_clipping(self):
        response = self.response()
        current = self.build_evidence("single-box-v2")
        self.add_handoff_health(current, "waiting")
        h = current["handoff_health"]
        h["items"].append(copy.deepcopy(h["items"][0]))
        h["items"][1]["target_id"] = "f"*64
        h["counts"].update(observed=2, complete=2, waiting_scopes=2, waiting_recovery_scopes=2)
        response["runtime_builds"] = current
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0)
        self.assertEqual(report["runtime_builds"]["handoff_health"], h)
        self.assertNotIn("pending", h["counts"])
        self.assertIn("no fleet total", (self.root/"summary").read_text())
        response["padding"] = "x"*((1 << 20)-len(json.dumps(response, indent=2).encode())-1+300)
        self.build_response_file(response, "single-box-v2")
        result, report = self.invoke()
        self.assertEqual(result.returncode, 0)
        self.assertEqual(report["runtime_builds"]["handoff_health"]["counts"], h["counts"])
        self.assertEqual(report["runtime_builds"]["evidence"], current["evidence"])
        self.assertGreater(report["runtime_builds"]["handoff_envelope_omitted_items"], 0)
        self.assertLessEqual(self.report.stat().st_size, 1 << 20)

    def test_build_recovery_history_survives_collection_and_summary(self):
        response = self.history_response()
        self.build_response_file(response)
        process, report = self.invoke()
        self.assertEqual(process.returncode, 0)
        self.assertFalse(report["had_confirmed_faults"])
        self.assertEqual(report["runtime_builds"], response["runtime_builds"])
        summary = (self.root / "summary").read_text()
        self.assertIn("Runtime builds (advisory): **matching**", summary)
        self.assertIn("earlier_mismatch=true, matching_throughout_observed_polls=false", summary)
        self.assertIn("first_mismatch_at", summary)
        self.assertIn("last_recovery_at", summary)

    def test_build_history_all_matching_deduplication_and_regressions(self):
        for states, events, regressions in ((("matching",)*25, 1, 0), (("mismatched", "matching"), 2, 0),
                                           (("matching", "unknown", "mixed", "matching", "matching"), 4, 1)):
            with self.subTest(states=states):
                response = self.history_response(states)
                self.build_response_file(response)
                process, report = self.invoke()
                self.assertEqual(process.returncode, 0)
                result = report["runtime_builds"]
                self.assertEqual(result, response["runtime_builds"])
                self.assertEqual(result["history"]["total_events"], events)
                self.assertEqual(result["progress"]["regressions"], regressions)
                self.assertEqual(result["progress"]["matching_throughout_observed_polls"], events == 1)

    def test_runtime_busy_reason_survives_ssh_collection_and_history(self):
        response = self.history_response(("unknown", "matching", "unknown"))
        current = response["runtime_builds"]
        current.update(reason="source_runtime_busy", source_http_status=429)
        history = current["history"]
        history["events"][0].update(reason="source_rate_limited", source_http_status=429)
        history["events"][-1].update(reason="source_runtime_busy", source_http_status=429)
        history["retained_bytes"] = builds.build_history_bytes(history["events"])
        self.build_response_file(response)
        process, report = self.invoke()
        self.assertEqual(process.returncode, 0)
        self.assertEqual(report["runtime_builds"], current)
        self.assertEqual(report["checks"], response["checks"])
        self.assertIn("source_runtime_busy", (self.root / "summary").read_text())

        bad_history = copy.deepcopy(response)
        bad_events = bad_history["runtime_builds"]["history"]["events"]
        bad_events[0].update(reason="source_runtime_busy", source_http_status=503)
        bad_history["runtime_builds"]["history"]["retained_bytes"] = builds.build_history_bytes(bad_events)
        self.build_response_file(bad_history)
        process, report = self.invoke()
        self.assertEqual(process.returncode, 0)
        self.assertEqual(report["runtime_builds"]["reason"], "source_runtime_busy")
        self.assertEqual(report["runtime_builds"]["history_unavailable_reason"], "invalid_history")

        for status in (None, 503):
            with self.subTest(status=status):
                bad = copy.deepcopy(response)
                bad_current = bad["runtime_builds"]
                if status is None:
                    del bad_current["source_http_status"]
                else:
                    bad_current["source_http_status"] = status
                self.build_response_file(bad)
                process, report = self.invoke()
                self.assertEqual(process.returncode, 0)
                self.assertEqual(report["runtime_builds"]["reason"], "invalid_advisory_evidence")
                self.assertEqual(report["checks"], response["checks"])

    def test_historical_source_clocks_are_not_reclassified_at_collection_time(self):
        after = (dt.datetime.now(dt.UTC)-dt.timedelta(minutes=10)).isoformat().replace("+00:00", "Z")
        response = self.history_response(ages=[240, 180, 1], after=after)
        self.env["MONITORING_NOT_BEFORE"] = after
        self.build_response_file(response)
        process, report = self.invoke()
        self.assertEqual(process.returncode, 0)
        self.assertEqual(report["runtime_builds"], response["runtime_builds"])
        self.assertEqual(report["runtime_builds"]["history"]["events"][0]["evidence_generated_at"], response["runtime_builds"]["history"]["events"][0]["evidence_generated_at"])

    def test_history_drops_preserve_sticky_first_observation_times(self):
        response = self.history_response(("matching", "mixed")*20+("matching",))
        value = response["runtime_builds"]
        history = value["history"]
        history["events"] = history["events"][2:]
        history.update(retained_events=len(history["events"]), envelope_omitted_events=2,
                       retained_bytes=builds.build_history_bytes(history["events"]), truncated=True)
        self.build_response_file(response)
        process, report = self.invoke()
        self.assertEqual(process.returncode, 0)
        self.assertEqual(report["runtime_builds"], value)
        self.assertGreater(history["dropped_events"], 0)
        self.assertEqual(report["runtime_builds"]["progress"]["regressions"], 20)
        self.assertLess(builds.timestamp(value["progress"]["first_mismatch_at"]), builds.timestamp(history["events"][0]["at"]))

    def test_malformed_history_does_not_discard_valid_current_build_evidence(self):
        valid = self.history_response()
        changes = [
            (("history", "events"), "private-provider-error"),
            (("history", "event_limit"), 17),
            (("history", "retained_bytes"), 1),
            (("history", "dropped_events"), 1),
            (("history", "truncated"), True),
            (("history", "events", 1, "reason"), "private-provider-error"),
            (("history", "events", 1, "observation"), 1),
            (("progress", "observations"), True),
            (("progress", "observations"), 4),
            (("progress", "matching_throughout_observed_polls"), True),
            (("progress", "last_matching_at"), "2000-01-01T00:00:00Z"),
            (("progress", "regressions"), -1),
        ]
        for path, value in changes:
            response = copy.deepcopy(valid)
            target = response["runtime_builds"]
            for part in path[:-1]:
                target = target[part]
            target[path[-1]] = value
            self.build_response_file(response)
            process, report = self.invoke()
            self.assertEqual(process.returncode, 0, path)
            result = report["runtime_builds"]
            self.assertEqual(result["state"], "matching", path)
            self.assertEqual(result["evidence"], valid["runtime_builds"]["evidence"], path)
            self.assertEqual(result["history_unavailable_reason"], "invalid_history", path)
            self.assertNotIn("history", result)
            self.assertNotIn("progress", result)

    def test_history_unknown_fields_are_redacted_without_losing_valid_events(self):
        response = self.history_response()
        value = response["runtime_builds"]
        expected = copy.deepcopy(value)
        for target in (value["progress"], value["history"], value["history"]["events"][0],
                       value["history"]["events"][0]["counts"], value["history"]["events"][0]["jobs"][0]):
            target["provider_debug"] = "fixture-private-token private-provider-error"
        self.build_response_file(response)
        process, report = self.invoke()
        self.assertEqual(process.returncode, 0)
        self.assertEqual(report["runtime_builds"], expected)

    def test_history_envelope_eviction_preserves_progress_and_current_diagnostics(self):
        response = self.history_response(("matching", "mixed")*10+("unknown",))
        value = response["runtime_builds"]
        response["padding"] = "x" * ((1 << 20)-len(json.dumps(response, indent=2).encode())-1+2000)
        source = self.build_response_file(response)
        self.assertLess(source.stat().st_size+1, 1 << 20)
        process, report = self.invoke()
        self.assertEqual(process.returncode, 0)
        result = report["runtime_builds"]
        self.assertEqual(result["progress"], value["progress"])
        self.assertEqual(result["state"], "unknown")
        self.assertEqual(result["source_http_status"], 503)
        self.assertGreater(result["history"]["envelope_omitted_events"], 0)
        self.assertEqual(result["history"]["total_events"], value["history"]["total_events"])
        self.assertEqual(result["history"]["dropped_events"], value["history"]["dropped_events"])
        self.assertLessEqual(self.report.stat().st_size, 1 << 20)

    def test_unobserved_build_history_does_not_invent_a_poll(self):
        response = self.response("unknown")
        now = dt.datetime.now(dt.UTC).isoformat().replace("+00:00", "Z")
        response.update(attempts=0, started_at=now, updated_at=now)
        response["runtime_builds"] = {
            "advisory": True, "state": "unknown", "reason": "awaiting_evidence",
            "progress": {"observations": 0, "regressions": 0, "matching_throughout_observed_polls": False,
                         "observed_incomplete_evidence": False, "observed_source_unavailable": False},
            "history": {"scope": "observed_build_state_and_coverage_counts", "event_limit": 16, "byte_limit": 16 << 10,
                        "total_events": 0, "retained_events": 0, "retained_bytes": 9, "dropped_events": 0,
                        "envelope_omitted_events": 0, "truncated": False, "events": []},
        }
        self.build_response_file(response, code=3)
        process, report = self.invoke()
        self.assertEqual(process.returncode, 3)
        self.assertEqual(report["runtime_builds"], response["runtime_builds"])

    def test_truncated_history_cannot_hide_the_current_source_gap(self):
        response = self.history_response(("matching", "unknown"))
        value = response["runtime_builds"]
        value["progress"].update(observed_source_unavailable=False, observed_incomplete_evidence=False)
        value["history"].update(events=[], retained_events=0, retained_bytes=9, envelope_omitted_events=2, truncated=True)
        self.build_response_file(response)
        process, report = self.invoke()
        self.assertEqual(process.returncode, 0)
        self.assertEqual(report["runtime_builds"]["state"], "unknown")
        self.assertEqual(report["runtime_builds"]["source_http_status"], 503)
        self.assertEqual(report["runtime_builds"]["history_unavailable_reason"], "invalid_history")

    def test_v2_profiles_preserve_extended_job_history_and_summary(self):
        for role, expected_jobs in (("single-box-v2", 6), ("control-plane-v2", 8)):
            with self.subTest(role=role):
                response = self.history_response(role=role)
                self.build_response_file(response, role)
                process, report = self.invoke()
                self.assertEqual(process.returncode, 0)
                value = report["runtime_builds"]
                self.assertEqual(value, response["runtime_builds"])
                self.assertEqual(len(value["evidence"]["jobs"]), expected_jobs)
                for event in value["history"]["events"]:
                    if "jobs" in event:
                        self.assertEqual([job["job"] for job in event["jobs"][-2:]], ["builderd", "imaged"])
                self.assertFalse(report["had_confirmed_faults"])
                summary = (self.root / "summary").read_text()
                self.assertIn("Role: `"+role+"`", summary)
                self.assertIn("Build evidence source builderd: available", summary)
                self.assertIn("Build evidence source imaged: available", summary)
                self.assertIn("earlier_mismatch=true", summary)

    def test_profile_versions_cannot_silently_change_job_scope(self):
        for requested, returned in (("single-box-v1", "single-box-v2"), ("single-box-v2", "single-box-v1"),
                                    ("control-plane-v1", "control-plane-v2"), ("control-plane-v2", "control-plane-v1")):
            with self.subTest(requested=requested, returned=returned):
                response = self.response()
                value = self.build_evidence(returned)
                value["evidence"]["expected_role"] = requested
                response["runtime_builds"] = value
                self.build_response_file(response, requested)
                process, report = self.invoke()
                self.assertEqual(process.returncode, 0)
                self.assertEqual(report["runtime_builds"]["reason"], "invalid_advisory_evidence")

    def test_v2_older_api_is_unknown_without_retrying_as_v1(self):
        response = self.response()
        response["runtime_builds"] = {"advisory": True, "state": "unknown", "reason": "source_http_error", "source_http_status": 400}
        self.build_response_file(response, "control-plane-v2")
        process, report = self.invoke()
        self.assertEqual(process.returncode, 0)
        self.assertEqual(report["runtime_builds"], response["runtime_builds"])
        remote = json.loads((self.root / "capture.json").read_text())["args"][-1]
        self.assertEqual(shlex.split(remote.splitlines()[1])[-2:], ["--expected-role", "control-plane-v2"])
        self.assertNotIn("control-plane-v1", remote)


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
