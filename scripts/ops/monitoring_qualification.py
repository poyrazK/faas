#!/usr/bin/env python3
"""Prepare/exercise production routes and record explicitly operator-attested receipts."""

import argparse
from datetime import datetime, timedelta, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import signal
import stat
import subprocess
import tempfile
import threading
from urllib.request import Request, build_opener, ProxyHandler

import monitoring_qualification_remote as remote


WORKFLOW = ".github/workflows/monitoring-qualification.yml"
SCOPE = "production_monitoring_qualification"
MAX_BYTES = 1 << 20
RECEIPTS_MAX_BYTES = 32768
HOLD_SECONDS = 90
VALID_HOURS = 24
EVENTS = {"faas-page": ("firing", "resolved"), "faas-warn": ("firing", "resolved"),
          "faas-heartbeat": ("watchdog_ping", "watchdog_alarm", "watchdog_recovery")}
LIMITATIONS = ["operator_attestation_not_provider_api_verification", "receiver_groups_can_aggregate_destinations",
               "injected_alerts_do_not_test_prometheus_rule_evaluation", "live_watchdog_interruption_not_exercised",
               "external_deadline_test_uses_separate_check", "validity_requires_unchanged_receiver_configuration",
               "release_pointer_does_not_prove_runtime_identity", "inventory_digest_does_not_identify_destinations"]
REMOTE_REASONS = {"invalid_timestamp", "inventory_unavailable", "receiver_api_unavailable", "invalid_receiver_inventory",
                  "required_receiver_missing", "invalid_operation", "source_unavailable", "response_too_large",
                  "active_release_unavailable", "invalid_test_identity", "invalid_alert_window", "alert_window_expired",
                  "operator_credential_unavailable", "active_release_changed", "receiver_inventory_changed", "invalid_request"}


def utcnow():
    return datetime.now(timezone.utc)


def require(condition, reason):
    if not condition:
        raise remote.QualificationError(reason)


def fullmatch(pattern, value):
    return isinstance(value, str) and re.fullmatch(pattern, value) is not None


def atomic_report(path, report):
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    name = None
    try:
        with tempfile.NamedTemporaryFile(mode="w", encoding="utf-8", dir=path.parent, prefix=".qualification-", delete=False) as output:
            name = output.name
            json.dump(report, output, indent=2, allow_nan=False)
            output.write("\n")
        os.replace(name, path)
    finally:
        if name and os.path.exists(name):
            os.unlink(name)


def closed_json(body):
    def reject(_):
        raise ValueError("invalid_json")
    def pairs(rows):
        out = {}
        for key, value in rows:
            if key in out:
                raise ValueError("duplicate_json_key")
            out[key] = value
        return out
    return json.loads(body, parse_constant=reject, object_pairs_hook=pairs)


def read_json(path):
    descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(descriptor, "rb") as source:
        require(stat.S_ISREG(os.fstat(source.fileno()).st_mode), "invalid_evidence_file")
        body = source.read(MAX_BYTES + 1)
    require(len(body) <= MAX_BYTES, "evidence_too_large")
    return closed_json(body), hashlib.sha256(body).hexdigest()


def configuration(env):
    config = {"fleet": env.get("QUALIFICATION_FLEET", ""), "run_id": env.get("GITHUB_RUN_ID", ""),
              "attempt": env.get("GITHUB_RUN_ATTEMPT", ""), "source_commit": env.get("GITHUB_SHA", ""),
              "repository": env.get("GITHUB_REPOSITORY", ""), "actor": env.get("GITHUB_ACTOR", "")}
    require(config["fleet"] in ("production", "production-us"), "invalid_fleet")
    require(fullmatch(r"[1-9][0-9]{0,19}", config["run_id"]) and fullmatch(r"[1-9][0-9]{0,5}", config["attempt"]), "invalid_workflow_identity")
    require(fullmatch(r"[0-9a-f]{40}", config["source_commit"]), "invalid_source_commit")
    require(fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", config["repository"]), "invalid_repository")
    require(fullmatch(r"[A-Za-z0-9][A-Za-z0-9_\[\]-]{0,63}", config["actor"]), "invalid_operator_identity")
    return config


def new_report(config, mode, now):
    return {"schema_version": 1, "scope": SCOPE, "qualification_id": "mq-" + config["run_id"] + "-" + config["attempt"],
            "fleet": config["fleet"], "mode": mode, "source_commit": config["source_commit"],
            "workflow": {"repository": config["repository"], "path": WORKFLOW, "run_id": config["run_id"], "attempt": config["attempt"]},
            "created_at": remote.stamp(now), "updated_at": remote.stamp(now), "valid_until": remote.stamp(now + timedelta(hours=VALID_HOURS)),
            "state": "unknown", "reason": "qualification_not_started", "release": None, "inventory_sha256": None,
            "expected_groups": [], "checks": [], "observations": [], "external_check": None, "attestation": None,
            "alert_starts_at": None, "alert_expires_at": None,
            "operations": {name: {"state": "not_started"} for name in ("prepare", "fire", "resolve")},
            "limitations": LIMITATIONS.copy()}


def checks_for(groups, observations):
    checks = []
    for group in groups:
        events = []
        for event in EVENTS[group["receiver"]]:
            found = next((row for row in observations if all(row.get(key) == group[key] for key in ("receiver", "integration")) and row["event"] == event), None)
            events.append({"event": event, "state": "unknown" if found is None else "operator_attested" if found["result"] == "received" else "failed"})
        states = [event["state"] for event in events]
        state = "failed" if "failed" in states else "unknown" if "unknown" in states else "operator_attested"
        checks.append({**group, "state": state, "events": events})
    return checks


class SSHRemote:
    def __init__(self, env):
        self.host = env.get("CP_HOST", "")
        self.key = env.get("QUALIFICATION_SSH_KEY", os.path.expanduser("~/.ssh/deploy_key"))
        self.token = env.get("MONITORING_OPERATOR_TOKEN", "")

    def __call__(self, operation, report):
        if not fullmatch(r"[A-Za-z0-9][A-Za-z0-9.:-]{0,252}", self.host):
            return {"state": "unknown", "reason": "ssh_host_unavailable"}
        if operation != "resolve" and (not self.token.strip() or len(self.token.encode()) > remote.TOKEN_MAX or any(char in self.token for char in "\r\n\x00")):
            return {"state": "unknown", "reason": "operator_credential_unavailable"}
        payload = {key: report[key] for key in ("qualification_id", "expected_groups", "inventory_sha256", "alert_starts_at", "alert_expires_at")}
        payload.update(operation=operation, operator_token=self.token if operation != "resolve" else "")
        if report.get("release"):
            payload["expected_release"] = report["release"]
        # Only reviewed code is in the command argument; credentials travel on
        # stdin. Disable PTY echo and discard raw remote/transport error text.
        code = Path(remote.__file__).read_text()
        command = ["ssh", "-T", "-i", self.key, "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes",
                   "-o", "ConnectTimeout=10", "-o", "ServerAliveInterval=5", "-o", "ServerAliveCountMax=2",
                   "root@" + self.host, "python3 -c " + shlex.quote(code)]
        child_env = os.environ.copy()
        child_env.pop("MONITORING_OPERATOR_TOKEN", None)
        child_env.pop("GITHUB_TOKEN", None)
        child_env.pop("QUALIFICATION_RECEIPTS", None)
        with tempfile.TemporaryFile() as output:
            try:
                result = subprocess.run(command, input=json.dumps(payload).encode(), stdout=output, stderr=subprocess.DEVNULL,
                                        timeout=30, check=False, env=child_env)
                if result.returncode not in (0, 3) or output.tell() > MAX_BYTES:
                    return {"state": "unknown", "reason": "transport_unavailable"}
                output.seek(0)
                value = closed_json(output.read())
                if value.get("state") == "accepted" and result.returncode == 0:
                    return validate_remote_result(value, operation)
                reason = value.get("reason")
                return {"state": "unknown", "reason": reason if reason in REMOTE_REASONS else "source_unavailable"}
            except remote.QualificationError:
                return {"state": "unknown", "reason": "invalid_remote_result"}
            except (OSError, ValueError, TypeError, AttributeError, subprocess.TimeoutExpired, RecursionError):
                return {"state": "unknown", "reason": "transport_unavailable"}


def validate_remote_result(value, operation):
    require(value.get("operation") == operation and remote.fresh(value.get("at"), utcnow()), "invalid_remote_result")
    result = {"operation": operation, "state": "accepted", "at": value["at"]}
    if operation != "resolve":
        require(fullmatch(r"[0-9a-f]{40}", value.get("release")), "invalid_remote_result")
        groups = reviewed_groups(value.get("expected_groups"))
        require(value.get("inventory_sha256") == remote.inventory_digest(groups), "invalid_remote_result")
        result.update(release=value["release"], expected_groups=groups, inventory_sha256=value["inventory_sha256"])
    return result


def reviewed_groups(groups):
    require(isinstance(groups, list) and 1 <= len(groups) <= remote.GROUPS_MAX, "invalid_receiver_inventory")
    for group in groups:
        require(isinstance(group, dict) and set(group) == {"receiver", "integration"} and group["receiver"] in remote.ROLES and group["integration"] in remote.INTEGRATIONS, "invalid_receiver_inventory")
        require(group["receiver"] != "faas-heartbeat" or group["integration"] == "webhook", "invalid_receiver_inventory")
    require(groups == sorted(groups, key=lambda row: (row["receiver"], row["integration"])) and len({(row["receiver"], row["integration"]) for row in groups}) == len(groups), "invalid_receiver_inventory")
    require({"faas-page", "faas-heartbeat"}.issubset({row["receiver"] for row in groups}), "required_receiver_missing")
    return groups


def exercise(report, rpc, save, stopped, clock=utcnow, hold=lambda stop: stop.wait(HOLD_SECONDS)):
    prepared = rpc("prepare", report)
    report["operations"]["prepare"] = prepared
    if prepared["state"] != "accepted":
        report["reason"] = prepared["reason"]
        save(report)
        return report
    for key in ("release", "expected_groups", "inventory_sha256"):
        report[key] = prepared[key]
    report["checks"] = checks_for(report["expected_groups"], [])
    report["reason"] = "receipts_unrecorded"
    save(report)
    if report["mode"] != "exercise" or stopped.is_set():
        if stopped.is_set():
            report["reason"] = "cancelled"
            save(report)
        return report
    now = clock()
    report["alert_starts_at"] = remote.stamp(now)
    report["alert_expires_at"] = remote.stamp(now + timedelta(seconds=remote.ALERT_LIFETIME_SECONDS))
    report["operations"]["fire"] = {"state": "attempting"}
    save(report)
    try:
        report["operations"]["fire"] = rpc("fire", report)
        save(report)
        if report["operations"]["fire"]["state"] == "accepted" and not stopped.is_set():
            hold(stopped)
    finally:
        # A timeout can occur after Alertmanager accepted the POST. Cleanup
        # always runs after any submission attempt, independent of its result.
        report["operations"]["resolve"] = rpc("resolve", report)
        report["updated_at"] = remote.stamp(clock())
        if stopped.is_set():
            report["reason"] = "cancelled"
        elif any(report["operations"][name]["state"] != "accepted" for name in ("fire", "resolve")):
            report["reason"] = "alert_submission_or_cleanup_unconfirmed"
        save(report)
    return report


def source_metadata(repository, run_id, token):
    require(fullmatch(r"[1-9][0-9]{0,19}", run_id) and bool(token), "source_metadata_unavailable")
    request = Request("https://api.github.com/repos/" + repository + "/actions/runs/" + run_id,
                      headers={"Authorization": "Bearer " + token, "Accept": "application/vnd.github+json"})
    try:
        with build_opener(ProxyHandler({}), remote.NoRedirect()).open(request, timeout=10) as response:
            body = response.read(MAX_BYTES + 1)
            require(len(body) <= MAX_BYTES, "source_metadata_unavailable")
            return closed_json(body)
    except (OSError, ValueError, RecursionError):
        raise remote.QualificationError("source_metadata_unavailable") from None


def validate_source(source, config, run_id, attempt, metadata, now):
    require(isinstance(source, dict) and source.get("schema_version") == 1 and source.get("scope") == SCOPE, "invalid_source_report")
    require(source.get("fleet") == config["fleet"] and source.get("mode") == "exercise", "source_identity_mismatch")
    workflow = {"repository": config["repository"], "path": WORKFLOW, "run_id": run_id, "attempt": attempt}
    require(source.get("workflow") == workflow and source.get("qualification_id") == "mq-" + run_id + "-" + attempt, "source_identity_mismatch")
    require(run_id != config["run_id"] and fullmatch(r"[1-9][0-9]{0,19}", run_id) and fullmatch(r"[1-9][0-9]{0,5}", attempt), "source_identity_mismatch")
    require(metadata.get("id") == int(run_id) and metadata.get("event") == "workflow_dispatch" and metadata.get("status") == "completed" and metadata.get("path", "").split("@", 1)[0] == WORKFLOW, "source_workflow_mismatch")
    require(metadata.get("head_sha") == source.get("source_commit") and fullmatch(r"[0-9a-f]{40}", source.get("source_commit")) and type(metadata.get("run_attempt")) is int and metadata["run_attempt"] >= int(attempt), "source_workflow_mismatch")
    created, until = remote.timestamp(source.get("created_at")), remote.timestamp(source.get("valid_until"))
    require(until - created == timedelta(hours=VALID_HOURS) and created <= now + timedelta(seconds=30) and now <= until, "qualification_expired")
    for operation in ("prepare", "fire", "resolve"):
        value = source.get("operations", {}).get(operation, {})
        require(value.get("state") == "accepted", "source_exercise_incomplete")
        remote.timestamp(value.get("at"))
    require(created - timedelta(seconds=30) <= remote.timestamp(source["operations"]["prepare"]["at"]) <= remote.timestamp(source["operations"]["fire"]["at"]) <= remote.timestamp(source["operations"]["resolve"]["at"]) <= now + timedelta(seconds=30), "invalid_exercise_order")
    require(source.get("limitations") == LIMITATIONS, "invalid_source_report")
    require(fullmatch(r"[0-9a-f]{40}", source.get("release")), "invalid_source_report")
    require(source.get("inventory_sha256") == remote.inventory_digest(reviewed_groups(source.get("expected_groups"))), "invalid_source_report")
    # Validate the saved identity and expiry using the same deterministic
    # generator used for cleanup; old alerts are never submitted here.
    remote.test_alerts(source, now, resolving=True)


def record_base(source, config, now):
    report = new_report(config, "record", now)
    for key in ("qualification_id", "source_commit", "workflow", "created_at", "valid_until", "release", "inventory_sha256", "expected_groups", "alert_starts_at", "alert_expires_at"):
        report[key] = source[key]
    report["operations"] = {key: {"state": "accepted", "at": source["operations"][key]["at"]} for key in ("prepare", "fire", "resolve")}
    report["checks"] = checks_for(source["expected_groups"], [])
    return report


def attest(source, manifest, config, current, source_hash, now):
    require(isinstance(manifest, dict) and set(manifest) == {"qualification_id", "fleet", "release", "inventory_sha256", "receiver_configuration_unchanged", "external_check", "observations"}, "invalid_receipt_manifest")
    for key in ("qualification_id", "fleet", "release", "inventory_sha256"):
        require(manifest.get(key) == source.get(key), "receipt_identity_mismatch")
    require(current.get("state") == "accepted" and all(current.get(key) == source.get(key) for key in ("release", "inventory_sha256", "expected_groups")), "current_release_or_inventory_changed")
    require(manifest["receiver_configuration_unchanged"] is True, "receiver_configuration_unverified")
    groups = source["expected_groups"]
    require(remote.inventory_digest(groups) == source["inventory_sha256"], "invalid_source_report")
    external = manifest["external_check"]
    require(isinstance(external, dict) and set(external) == {"method", "check_id_sha256", "deadline_seconds", "same_notification_settings"}, "invalid_external_check")
    require(external["method"] == "dedicated_deadline_check" and fullmatch(r"[0-9a-f]{64}", external["check_id_sha256"]) and type(external["deadline_seconds"]) is int and 1 <= external["deadline_seconds"] <= 3600 and external["same_notification_settings"] is True, "invalid_external_check")
    rows = manifest["observations"]
    require(isinstance(rows, list) and len(rows) <= 2 * remote.GROUPS_MAX + 3, "invalid_receipts")
    observations, seen, hashes = [], set(), set()
    expected = {(row["receiver"], row["integration"]) for row in groups}
    for row in rows:
        require(isinstance(row, dict) and set(row) == {"receiver", "integration", "event", "observed_at", "result", "receipt_sha256", "all_destinations_observed"}, "invalid_receipts")
        receiver, integration, event = row["receiver"], row["integration"], row["event"]
        require(isinstance(receiver, str) and isinstance(integration, str) and isinstance(event, str) and (receiver, integration) in expected and event in EVENTS[receiver], "unexpected_receipt")
        key = (receiver, integration, event)
        require(key not in seen and row["result"] in ("received", "not_received"), "duplicate_or_invalid_receipt")
        seen.add(key)
        at = remote.timestamp(row["observed_at"])
        earliest = source["created_at"] if receiver == "faas-heartbeat" else source["operations"]["fire" if event == "firing" else "resolve"]["at"]
        require(remote.timestamp(earliest) <= at <= min(now + timedelta(seconds=30), remote.timestamp(source["valid_until"])), "receipt_outside_test_window")
        if row["result"] == "received":
            require(fullmatch(r"[0-9a-f]{64}", row["receipt_sha256"]) and row["receipt_sha256"] not in hashes and row["all_destinations_observed"] is True, "invalid_receipt_identity")
            hashes.add(row["receipt_sha256"])
        else:
            require(row["receipt_sha256"] is None and row["all_destinations_observed"] is False, "invalid_receipt_identity")
        observations.append({**row, "observed_at": remote.stamp(at)})
    for group in groups:
        received = {row["event"]: remote.timestamp(row["observed_at"]) for row in observations if row["result"] == "received" and all(row[key] == group[key] for key in ("receiver", "integration"))}
        if group["receiver"] == "faas-heartbeat":
            if "watchdog_ping" in received and "watchdog_alarm" in received:
                require(received["watchdog_alarm"] >= received["watchdog_ping"] + timedelta(seconds=external["deadline_seconds"]), "deadline_not_exercised")
            if "watchdog_alarm" in received and "watchdog_recovery" in received:
                require(received["watchdog_recovery"] > received["watchdog_alarm"], "invalid_receipt_order")
        elif {"firing", "resolved"}.issubset(received):
            require(received["resolved"] > received["firing"], "invalid_receipt_order")
    # Build a closed report rather than forwarding arbitrary source fields.
    report = record_base(source, config, now)
    report["observations"], report["external_check"] = observations, external
    report["checks"] = checks_for(groups, observations)
    states = [row["state"] for row in report["checks"]]
    report["state"] = "failed" if "failed" in states else "unknown" if "unknown" in states else "operator_attested"
    report["reason"] = {"failed": "operator_reported_missing_receipt", "unknown": "receipts_unrecorded", "operator_attested": ""}[report["state"]]
    report["attestation"] = {"actor": config["actor"], "run_id": config["run_id"], "attempt": config["attempt"],
                             "source_artifact_sha256": source_hash, "recorded_at": remote.stamp(now),
                             "recorder_commit": config["source_commit"], "receiver_configuration_unchanged": True}
    return report


def emit_summary(report):
    text = "### Production monitoring qualification\n\n"
    text += "Fleet: `" + report["fleet"] + "`; test: `" + report["qualification_id"] + "`.\n\n"
    text += "Result: **" + report["state"] + "** (" + (report["reason"] or "operator-attested receipts") + ").\n\n"
    text += "Receipt attestations do not independently verify provider APIs or live-watchdog interruption.\n\n"
    for check in report["checks"]:
        text += "- " + check["receiver"] + "/" + check["integration"] + ": " + check["state"] + "\n"
    if os.environ.get("GITHUB_STEP_SUMMARY"):
        with open(os.environ["GITHUB_STEP_SUMMARY"], "a", encoding="utf-8") as output:
            output.write(text)
    print("monitoring-qualification: " + report["state"] + " reason=" + (report["reason"] or "operator_attested"))


def receipt_template(report):
    return {**{key: report[key] for key in ("qualification_id", "fleet", "release", "inventory_sha256")},
            "receiver_configuration_unchanged": False,
            "external_check": {"method": "dedicated_deadline_check", "check_id_sha256": None,
                               "deadline_seconds": 60, "same_notification_settings": False},
            "observations": [{**group, "event": event, "observed_at": None, "result": "not_recorded",
                              "receipt_sha256": None, "all_destinations_observed": False}
                             for group in report["expected_groups"] for event in EVENTS[group["receiver"]]]}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--mode", choices=("initialize", "prepare", "exercise", "record"), default="prepare")
    parser.add_argument("--source", type=Path)
    args = parser.parse_args()
    config = configuration(os.environ)
    path = Path(os.environ.get("QUALIFICATION_REPORT", "monitoring-qualification.json"))
    report = new_report(config, args.mode, utcnow())
    save = lambda value: atomic_report(path, value)
    save(report)
    if args.mode == "initialize":
        return 0
    stopped = threading.Event()
    signal.signal(signal.SIGTERM, lambda *_: stopped.set())
    signal.signal(signal.SIGINT, lambda *_: stopped.set())
    rpc = SSHRemote(os.environ)
    try:
        require(os.environ.get("GITHUB_EVENT_NAME") == "workflow_dispatch", "manual_dispatch_required")
        if args.mode == "record":
            require(args.source is not None, "source_report_required")
            source, source_hash = read_json(args.source)
            run_id, attempt = os.environ.get("QUALIFICATION_SOURCE_RUN", ""), os.environ.get("QUALIFICATION_SOURCE_ATTEMPT", "")
            metadata = source_metadata(config["repository"], run_id, os.environ.get("GITHUB_TOKEN", ""))
            validate_source(source, config, run_id, attempt, metadata, utcnow())
            report = record_base(source, config, utcnow())
            body = os.environ.get("QUALIFICATION_RECEIPTS", "")
            require(len(body.encode()) <= RECEIPTS_MAX_BYTES, "receipt_manifest_too_large")
            current = rpc("prepare", source)
            report = attest(source, closed_json(body), config, current, source_hash, utcnow())
        else:
            report = exercise(report, rpc, save, stopped)
    except remote.QualificationError as error:
        report["state"], report["reason"] = "unknown", str(error)
    except (OSError, ValueError, TypeError, KeyError, AttributeError, OverflowError, RecursionError):
        report["state"], report["reason"] = "unknown", "qualification_unavailable"
    if stopped.is_set():
        report["state"], report["reason"] = "unknown", "cancelled"
    report["updated_at"] = remote.stamp(utcnow())
    save(report)
    if args.mode in ("prepare", "exercise") and report["expected_groups"]:
        atomic_report(path.with_name("receipt-template.json"), receipt_template(report))
    emit_summary(report)
    return {"operator_attested": 0, "failed": 1, "unknown": 3}[report["state"]]


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except remote.QualificationError as error:
        print("monitoring-qualification: " + str(error))
        raise SystemExit(2) from None
    except OSError:
        print("monitoring-qualification: report_io_failed")
        raise SystemExit(2) from None
