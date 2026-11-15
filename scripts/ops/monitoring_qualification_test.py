#!/usr/bin/env python3
"""Offline qualification tests: local HTTP, stubbed SSH, no production sends."""

import copy
from datetime import datetime, timedelta, timezone
import hashlib
import http.server
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import unittest
from unittest import mock
from urllib.request import Request

import monitoring_qualification as qualification
import monitoring_qualification_remote as remote


ROOT = Path(__file__).resolve().parents[2]
NOW = datetime.now(timezone.utc).replace(microsecond=0)
RELEASE = "a" * 40
CONFIG = {"fleet": "production", "run_id": "100", "attempt": "1", "source_commit": "b" * 40, "repository": "example/gregale", "actor": "operator"}
GROUPS = [{"receiver": "faas-heartbeat", "integration": "webhook"}, {"receiver": "faas-page", "integration": "slack"}, {"receiver": "faas-warn", "integration": "slack"}]


def inventory_body():
    signal = lambda value: {"state": "healthy", "value": value, "observed_at": remote.stamp(NOW)}
    return {"generated_at": remote.stamp(NOW), "receiver_health": {"collector": signal(1), "inventory": signal(len(GROUPS)),
            "groups": [{**group, "expected": True, "state": "partial", "reason": "success_not_observed"} for group in GROUPS]}}


def accepted(operation, at=NOW):
    out = {"operation": operation, "state": "accepted", "at": remote.stamp(at)}
    if operation != "resolve":
        out.update(release=RELEASE, expected_groups=copy.deepcopy(GROUPS), inventory_sha256=remote.inventory_digest(GROUPS))
    return out


def source_report():
    report = qualification.new_report(CONFIG, "exercise", NOW)
    report.update(release=RELEASE, expected_groups=copy.deepcopy(GROUPS), inventory_sha256=remote.inventory_digest(GROUPS),
                  alert_starts_at=remote.stamp(NOW + timedelta(seconds=1)),
                  alert_expires_at=remote.stamp(NOW + timedelta(seconds=901)), reason="receipts_unrecorded")
    report["operations"] = {operation: accepted(operation, NOW + timedelta(seconds=offset)) for operation, offset in (("prepare", 0), ("fire", 1), ("resolve", 91))}
    report["checks"] = qualification.checks_for(GROUPS, [])
    return report


def manifest():
    out = qualification.receipt_template(source_report())
    out["receiver_configuration_unchanged"] = True
    out["external_check"].update(check_id_sha256="d" * 64, same_notification_settings=True)
    offsets = {"firing": 20, "resolved": 4000, "watchdog_ping": 120, "watchdog_alarm": 181, "watchdog_recovery": 200}
    for row in out["observations"]:
        row.update(observed_at=remote.stamp(NOW + timedelta(seconds=offsets[row["event"]])), result="received",
                   receipt_sha256=hashlib.sha256((row["receiver"] + row["integration"] + row["event"]).encode()).hexdigest(), all_destinations_observed=True)
    return out


class InventoryAndAlertTest(unittest.TestCase):
    def test_unknown_success_does_not_block_qualification_inventory(self):
        body = inventory_body()
        body["private-provider-field"] = "https://private.example/token"
        body["receiver_health"]["groups"][0]["destination"] = "private-address"
        self.assertEqual(remote.receiver_inventory(body, NOW), GROUPS)

    def test_inventory_fails_closed(self):
        for mutate in (
            lambda b: b.update(generated_at=remote.stamp(NOW - timedelta(minutes=3))),
            lambda b: b.pop("receiver_health"),
            lambda b: b["receiver_health"]["collector"].update(value=0),
            lambda b: b["receiver_health"]["inventory"].update(value=True),
            lambda b: b["receiver_health"]["inventory"].update(value=4),
            lambda b: b["receiver_health"]["groups"][0].update(expected=None),
            lambda b: b["receiver_health"]["groups"][0].update(receiver="private-role"),
            lambda b: b["receiver_health"]["groups"][0].update(integration="private-destination"),
            lambda b: b["receiver_health"]["groups"][0].update(integration="slack"),
            lambda b: b["receiver_health"]["groups"].__setitem__(1, copy.deepcopy(b["receiver_health"]["groups"][0])),
        ):
            body = inventory_body()
            mutate(body)
            with self.assertRaises(remote.QualificationError):
                remote.receiver_inventory(body, NOW)

    def test_alert_identity_expiry_and_resolution(self):
        plan = source_report()
        fired = remote.test_alerts(plan, NOW + timedelta(seconds=2))
        resolved = remote.test_alerts(plan, NOW + timedelta(seconds=91), resolving=True)
        self.assertEqual(len(fired), 2)
        self.assertEqual([row["labels"] for row in fired], [row["labels"] for row in resolved])
        self.assertEqual([row["startsAt"] for row in fired], [row["startsAt"] for row in resolved])
        self.assertNotEqual(fired[0]["labels"]["family"], fired[1]["labels"]["family"])
        for row in fired:
            self.assertIn(plan["qualification_id"].replace("-", "_"), row["labels"]["alertname"])
            self.assertNotEqual(row["labels"]["alertname"], "FaasWatchdog")
            self.assertEqual(remote.timestamp(row["endsAt"]) - remote.timestamp(row["startsAt"]), timedelta(minutes=15))
        for mutate in (lambda p: p.update(qualification_id="; unsafe"),
                       lambda p: p.update(alert_expires_at=remote.stamp(NOW + timedelta(hours=1)))):
            bad = copy.deepcopy(plan)
            mutate(bad)
            with self.assertRaises(remote.QualificationError):
                remote.test_alerts(bad, NOW)
        with self.assertRaises(remote.QualificationError):
            remote.test_alerts(plan, NOW + timedelta(minutes=5))
        self.assertEqual(len(remote.test_alerts(plan, NOW + timedelta(hours=1), resolving=True)), 2)

    def test_fire_requires_pinned_release_and_inventory(self):
        plan = source_report()
        payload = {**plan, "operation": "fire", "operator_token": "fixture-token", "expected_release": RELEASE}
        calls = []
        def request(kind, **kwargs):
            calls.append((kind, kwargs))
            return inventory_body() if kind == "inventory" else None
        result = remote.operate(payload, now=NOW + timedelta(seconds=2), read_release=lambda: RELEASE, request=request)
        self.assertEqual(result["state"], "accepted")
        self.assertEqual([row[0] for row in calls], ["inventory", "alerts"])
        for field, value in (("expected_release", "c" * 40), ("inventory_sha256", "c" * 64), ("operator_token", "bad\ncredential")):
            calls.clear()
            with self.assertRaises(remote.QualificationError):
                remote.operate({**payload, field: value}, now=NOW + timedelta(seconds=2), read_release=lambda: RELEASE, request=request)
            self.assertNotIn("alerts", [row[0] for row in calls])

    def test_resolve_does_not_depend_on_current_inventory_or_release(self):
        def unavailable():
            self.fail("cleanup read a release pointer")
        calls = []
        def request(kind, **kwargs):
            self.assertEqual(kind, "alerts")
            calls.append(kwargs)
        result = remote.operate({**source_report(), "operation": "resolve"}, now=NOW + timedelta(seconds=100), read_release=unavailable, request=request)
        self.assertEqual(result["state"], "accepted")
        self.assertEqual(len(calls[0]["alerts"]), 2)


class ExerciseLifecycleTest(unittest.TestCase):
    def run_exercise(self, *, mode="exercise", fail=None, cancel=False):
        report = qualification.new_report(CONFIG, mode, NOW)
        calls, saves = [], []
        stopped = threading.Event()
        def rpc(operation, _):
            calls.append(operation)
            return {"state": "unknown", "reason": "source_unavailable"} if operation == fail else accepted(operation)
        def hold(stop):
            if cancel:
                stop.set()
        result = qualification.exercise(report, rpc, lambda value: saves.append(copy.deepcopy(value)), stopped, clock=lambda: NOW, hold=hold)
        return result, calls, saves

    def test_prepare_never_sends(self):
        report, calls, _ = self.run_exercise(mode="prepare")
        self.assertEqual(calls, ["prepare"])
        self.assertEqual(report["state"], "unknown")
        self.assertTrue(all(row["state"] == "unknown" for row in report["checks"]))

    def test_exercise_and_cancel_always_cleanup(self):
        for fail, cancel in ((None, False), ("fire", False), ("resolve", False), (None, True)):
            with self.subTest(fail=fail, cancel=cancel):
                report, calls, saves = self.run_exercise(fail=fail, cancel=cancel)
                self.assertEqual(calls, ["prepare", "fire", "resolve"])
                self.assertEqual(report["state"], "unknown")
                self.assertTrue(any(row["operations"]["fire"]["state"] == "attempting" for row in saves))
                if cancel:
                    self.assertEqual(report["reason"], "cancelled")

    def test_unavailable_prepare_never_sends(self):
        _, calls, _ = self.run_exercise(fail="prepare")
        self.assertEqual(calls, ["prepare"])

    def test_exception_after_post_still_resolves(self):
        calls = []
        def rpc(operation, _):
            calls.append(operation)
            if operation == "fire":
                raise OSError("private transport detail")
            return accepted(operation)
        with self.assertRaises(OSError):
            qualification.exercise(qualification.new_report(CONFIG, "exercise", NOW), rpc, lambda _: None, threading.Event(), clock=lambda: NOW)
        self.assertEqual(calls, ["prepare", "fire", "resolve"])


class ReceiptAttestationTest(unittest.TestCase):
    def setUp(self):
        self.source = source_report()
        self.config = {**CONFIG, "run_id": "101"}
        self.now = NOW + timedelta(hours=2)
        self.metadata = {"id": 100, "event": "workflow_dispatch", "status": "completed", "path": qualification.WORKFLOW,
                         "head_sha": CONFIG["source_commit"], "run_attempt": 1}

    def record(self, receipts=None, current=None):
        return qualification.attest(self.source, receipts if receipts is not None else manifest(), self.config,
                                    current if current is not None else accepted("prepare", self.now), "e" * 64, self.now)

    def test_complete_operator_attestation_is_explicit(self):
        qualification.validate_source(self.source, self.config, "100", "1", self.metadata, self.now)
        report = self.record()
        self.assertEqual(report["state"], "operator_attested")
        self.assertEqual(report["attestation"]["actor"], "operator")
        self.assertEqual(report["attestation"]["source_artifact_sha256"], "e" * 64)
        self.assertIn("live_watchdog_interruption_not_exercised", report["limitations"])
        self.assertTrue(all(row["state"] == "operator_attested" for row in report["checks"]))

    def test_partial_and_negative_receipts_remain_visible(self):
        receipts = manifest()
        receipts["observations"].pop()
        self.assertEqual(self.record(receipts)["state"], "unknown")
        receipts["observations"][0].update(result="not_received", receipt_sha256=None, all_destinations_observed=False)
        report = self.record(receipts)
        self.assertEqual(report["state"], "failed")
        self.assertEqual(report["checks"][0]["state"], "failed")

    def test_manifest_binding_and_receipt_validation(self):
        mutations = (
            lambda m: m.update(qualification_id="mq-999-1"),
            lambda m: m.update(fleet="production-us"),
            lambda m: m.update(release="f" * 40),
            lambda m: m.update(inventory_sha256="f" * 64),
            lambda m: m.update(receiver_configuration_unchanged=False),
            lambda m: m.update(raw_provider_url="https://private/token"),
            lambda m: m["external_check"].update(method="provider_test_notification"),
            lambda m: m["external_check"].update(same_notification_settings=False),
            lambda m: m["external_check"].update(check_id_sha256="https://private/token"),
            lambda m: m["observations"][0].update(receipt_sha256="https://private/token"),
            lambda m: m["observations"][0].update(all_destinations_observed=False),
            lambda m: m["observations"][0].update(observed_at=remote.stamp(NOW - timedelta(days=1))),
            lambda m: m["observations"][0].update(observed_at=remote.stamp(NOW + timedelta(days=1))),
            lambda m: m["observations"][0].update(receiver="private-receiver"),
            lambda m: m["observations"].append(copy.deepcopy(m["observations"][0])),
            lambda m: m["observations"][1].update(receipt_sha256=m["observations"][0]["receipt_sha256"]),
            lambda m: m["observations"][1].update(observed_at=remote.stamp(NOW + timedelta(seconds=130))),
        )
        for index, mutate in enumerate(mutations):
            with self.subTest(index=index):
                receipts = manifest()
                mutate(receipts)
                with self.assertRaises(remote.QualificationError):
                    self.record(receipts)

    def test_current_source_and_replay_checks(self):
        for field, value in (("release", "f" * 40), ("inventory_sha256", "f" * 64), ("state", "unknown")):
            current = accepted("prepare", self.now)
            current[field] = value
            with self.assertRaises(remote.QualificationError):
                self.record(current=current)
        for field, value in (("path", ".github/workflows/other.yml"), ("event", "push"), ("status", "in_progress"), ("head_sha", "f" * 40), ("run_attempt", 0), ("id", 999)):
            with self.assertRaises(remote.QualificationError):
                qualification.validate_source(self.source, self.config, "100", "1", {**self.metadata, field: value}, self.now)
        with self.assertRaises(remote.QualificationError):
            qualification.validate_source(self.source, self.config, "100", "1", self.metadata, NOW + timedelta(days=2))
        self.source["mode"] = "prepare"
        with self.assertRaises(remote.QualificationError):
            qualification.validate_source(self.source, self.config, "100", "1", self.metadata, self.now)

    def test_extra_source_data_is_not_forwarded(self):
        self.source["private_provider_error"] = "private-provider-detail"
        self.source["operations"]["fire"]["provider_url"] = "private-provider-detail"
        self.assertNotIn("private-provider-detail", json.dumps(self.record()))

    def test_record_entrypoint_receipts_and_exit_states(self):
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "source.json"
            output = Path(directory) / "report.json"
            source.write_text(json.dumps(self.source))
            original = source.read_bytes()
            for outcome, code in (("complete", 0), ("partial", 3), ("missing", 1), ("invalid", 3), ("cancelled", 3)):
                with self.subTest(outcome=outcome):
                    receipts = manifest()
                    if outcome == "partial":
                        receipts["observations"].pop()
                    if outcome == "missing":
                        receipts["observations"][0].update(result="not_received", receipt_sha256=None, all_destinations_observed=False)
                    if outcome == "invalid":
                        receipts["qualification_id"] = "mq-999-1"
                    env = {"QUALIFICATION_FLEET": "production", "QUALIFICATION_REPORT": str(output),
                           "GITHUB_RUN_ID": "101", "GITHUB_RUN_ATTEMPT": "1", "GITHUB_SHA": CONFIG["source_commit"],
                           "GITHUB_REPOSITORY": CONFIG["repository"], "GITHUB_ACTOR": "operator", "GITHUB_EVENT_NAME": "workflow_dispatch",
                           "QUALIFICATION_SOURCE_RUN": "100", "QUALIFICATION_SOURCE_ATTEMPT": "1", "GITHUB_TOKEN": "fixture-token",
                           "QUALIFICATION_RECEIPTS": json.dumps(receipts)}
                    rpc = mock.Mock(return_value=accepted("prepare", self.now))
                    stopped = threading.Event()
                    if outcome == "cancelled":
                        stopped.set()
                    with mock.patch.dict(os.environ, env), mock.patch.object(sys, "argv", ["qualify", "--mode", "record", "--source", str(source)]), \
                            mock.patch.object(qualification, "utcnow", return_value=self.now), mock.patch.object(qualification, "source_metadata", return_value=self.metadata), \
                            mock.patch.object(qualification, "SSHRemote", return_value=rpc), mock.patch.object(qualification.signal, "signal"), \
                            mock.patch.object(qualification.threading, "Event", return_value=stopped), mock.patch.object(qualification, "emit_summary"):
                        self.assertEqual(qualification.main(), code)
                    saved = json.loads(output.read_text())
                    self.assertEqual(saved["qualification_id"], self.source["qualification_id"])
                    self.assertEqual(len(saved["checks"]), len(GROUPS))
                    if outcome == "cancelled":
                        self.assertEqual(saved["reason"], "cancelled")
                    self.assertEqual(output.stat().st_mode & 0o777, 0o600)
                    self.assertEqual(source.read_bytes(), original)
                    self.assertEqual([call.args[0] for call in rpc.call_args_list], ["prepare"])


class LoopbackTransportTest(unittest.TestCase):
    def test_fixed_urls_no_redirects_or_proxies(self):
        calls = []
        redirect = [False]
        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass
            def do_GET(self):
                calls.append((self.path, self.headers.get("Authorization")))
                if redirect[0]:
                    self.send_response(307)
                    self.send_header("Location", "/private-redirect")
                    self.end_headers()
                    return
                self.send_response(200)
                self.end_headers()
                self.wfile.write(json.dumps(inventory_body()).encode())
            def do_POST(self):
                calls.append((self.path, json.loads(self.rfile.read(int(self.headers["Content-Length"])))))
                self.send_response(200)
                self.end_headers()
        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        def request(url, **kwargs):
            self.assertIn(url, ("http://127.0.0.1:8080/v1/admin/obs/notification-health", "http://127.0.0.1:9094/api/v2/alerts"))
            path = "/v1/admin/obs/notification-health" if ":8080/" in url else "/api/v2/alerts"
            return Request("http://127.0.0.1:" + str(server.server_port) + path, **kwargs)
        with mock.patch.object(remote, "Request", side_effect=request), mock.patch.dict(os.environ, {"http_proxy": "http://private-proxy.invalid:9"}):
            self.assertEqual(remote.request_json("inventory", token="fixture-token"), inventory_body())
            remote.request_json("alerts", alerts=remote.test_alerts(source_report(), NOW + timedelta(seconds=2)))
            redirect[0] = True
            with self.assertRaises(remote.QualificationError):
                remote.request_json("inventory", token="fixture-token")
        self.assertEqual(calls[0][1], "Bearer fixture-token")
        self.assertEqual(calls[1][0], "/api/v2/alerts")
        self.assertNotIn("/private-redirect", [row[0] for row in calls])


class SSHAndWorkflowTest(unittest.TestCase):
    def test_ssh_stdin_projection_and_private_report(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            stub = root / "ssh"
            stub.write_text("""#!/usr/bin/env python3
import json, os, pathlib, sys
payload = json.load(sys.stdin)
pathlib.Path(os.environ['CAPTURE']).write_text(json.dumps({'args': sys.argv, 'payload': payload, 'env_has_token': 'MONITORING_OPERATOR_TOKEN' in os.environ, 'env_has_github_token': 'GITHUB_TOKEN' in os.environ}))
print(os.environ['RESPONSE'])
print('private-provider-error', file=sys.stderr)
""")
            stub.chmod(0o700)
            env = {**os.environ, "PATH": str(root) + os.pathsep + os.environ["PATH"], "CP_HOST": "cp.example.test", "MONITORING_OPERATOR_TOKEN": "fixture-private-token",
                   "GITHUB_TOKEN": "fixture-github-token", "CAPTURE": str(root / "capture"), "RESPONSE": json.dumps(accepted("prepare", datetime.now(timezone.utc)))}
            with mock.patch.dict(os.environ, env):
                result = qualification.SSHRemote(env)("prepare", qualification.new_report(CONFIG, "prepare", NOW))
            self.assertEqual(result["state"], "accepted")
            capture = json.loads((root / "capture").read_text())
            self.assertEqual(capture["payload"]["operator_token"], "fixture-private-token")
            self.assertFalse(capture["env_has_token"])
            self.assertFalse(capture["env_has_github_token"])
            self.assertNotIn("fixture-private-token", " ".join(capture["args"]))
            self.assertIn("-T", capture["args"])
            self.assertIn("StrictHostKeyChecking=yes", capture["args"])
            path = root / "report.json"
            qualification.atomic_report(path, result)
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            self.assertNotIn("private-provider-error", path.read_text())

    def test_workflow_is_manual_and_uploads_unknown(self):
        workflow = (ROOT / qualification.WORKFLOW).read_text()
        self.assertIn("workflow_dispatch:", workflow)
        self.assertNotIn("schedule:", workflow)
        self.assertNotIn("workflow_run:", workflow)
        self.assertIn("default: prepare", workflow)
        self.assertIn("if: always()", workflow)
        self.assertIn("cancel-in-progress: false", workflow)
        self.assertIn("retention-days: 30", workflow)
        self.assertNotIn("continue-on-error:", workflow)
        self.assertLess(workflow.index("Initialize unknown qualification evidence"), workflow.index("Set up pinned control-plane SSH"))
        for action in ("checkout", "upload-artifact", "download-artifact"):
            self.assertRegex(workflow, "actions/" + action + r"@[0-9a-f]{40}")

    def test_manual_guard_and_initial_report_via_real_entrypoint(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "report.json"
            env = {**os.environ, "QUALIFICATION_FLEET": CONFIG["fleet"], "QUALIFICATION_REPORT": str(path),
                   "GITHUB_RUN_ID": "100", "GITHUB_RUN_ATTEMPT": "1", "GITHUB_SHA": CONFIG["source_commit"],
                   "GITHUB_REPOSITORY": CONFIG["repository"], "GITHUB_ACTOR": "operator", "GITHUB_EVENT_NAME": "schedule",
                   "GITHUB_STEP_SUMMARY": str(Path(directory) / "summary.md")}
            command = [sys.executable, str(ROOT / "scripts/ops/monitoring_qualification.py")]
            result = subprocess.run(command + ["--mode", "initialize"], env=env, capture_output=True, text=True, check=False, timeout=10)
            self.assertEqual(result.returncode, 0)
            result = subprocess.run(command + ["--mode", "exercise"], env=env, capture_output=True, text=True, check=False, timeout=10)
            self.assertEqual(result.returncode, 3)
            report = json.loads(path.read_text())
            self.assertEqual(report["reason"], "manual_dispatch_required")
            self.assertTrue(all(row["state"] == "not_started" for row in report["operations"].values()))

    def test_source_files_and_json_are_bounded_and_not_symlinks(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "source"
            path.write_text(json.dumps(source_report()))
            report, digest = qualification.read_json(path)
            self.assertEqual(report["scope"], qualification.SCOPE)
            self.assertEqual(len(digest), 64)
            link = path.with_name("link")
            link.symlink_to(path)
            with self.assertRaises(OSError):
                qualification.read_json(link)
            path.write_bytes(b"x" * (qualification.MAX_BYTES + 1))
            with self.assertRaises(remote.QualificationError):
                qualification.read_json(path)
        for body in ('{"a":1,"a":2}', '{"value":NaN}'):
            with self.assertRaises(ValueError):
                qualification.closed_json(body)


if __name__ == "__main__":
    unittest.main()
