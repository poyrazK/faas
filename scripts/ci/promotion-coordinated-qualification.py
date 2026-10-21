#!/usr/bin/env python3
"""Validate supplemental coordinated native evidence; never enables admission."""
import argparse
import importlib.util
import json
from pathlib import Path
import re

spec = importlib.util.spec_from_file_location("promotion_base", Path(__file__).with_name("promotion-qualification.py"))
base = importlib.util.module_from_spec(spec)
spec.loader.exec_module(base)
META = "TestProjectEnvironmentPromotionCoordinatedFixtureMeta"
PREFIX = "TestProjectEnvironmentPromotionCoordinated"
SCENARIOS = {PREFIX + name + "Metal": "coordinated_" + name.lower() for name in ("Success", "Rollback", "Resolve")}
TESTS = set(SCENARIOS) | {META}
GROUP = "promotion-coordinated"


def integer(item, field, minimum):
    return type(item.get(field)) is int and item[field] >= minimum


def finish(item, identity, baseline, rollback, errors):
    base.require_proofs(item, ("restart_preflight", "restart_verifying", "candidate_recovered", "destination_secrets", "artifact_reused"), errors, identity)
    if not integer(item, "preflight_rounds", 2) or not integer(item, "candidate_jobs", 5):
        raise ValueError("missing real worker/probe rounds")
    cutover, selected = item["cutover_graph"], item["selected_graph"]
    if not base.valid_uuid(cutover) or cutover in (baseline, identity) or not base.valid_uuid(selected):
        raise ValueError("invalid cutover identity")
    if item["phase"] != ("failed" if rollback else "succeeded") or item["graph_config_restored"] is not rollback:
        raise ValueError("wrong terminal outcome")
    if rollback:
        if not integer(item, "regressed_rounds", 2) or item["worker_regression"] is not True or selected in (baseline, cutover, identity):
            raise ValueError("worker regression did not restore full graph")
    else:
        if selected != cutover:
            raise ValueError("replacement graph not selected")
        base.healthy_windows(item, errors, identity, item["health_started_at"])
    hashes = [item[field] for field in ("destination_config_hash", "baseline_config_hash")]
    if len(set(hashes)) != 2 or not all(isinstance(h, str) and re.fullmatch(r"[a-f0-9]{64}", h) for h in hashes) or item["selected_config_hash"] != hashes[1 if rollback else 0]:
        raise ValueError("mixed selected graph and configuration")
    report = item["private_report"]
    fields = ("deployment_id", "backend_deployment_id", "instance_id", "backend_instance_id")
    values = [report[field] for field in fields] + [item["worker_deployment"], item["worker_instance"]]
    if not all(base.valid_uuid(value) for value in values) or len(set(values)) != len(values):
        raise ValueError("mixed runtime identities")
    if report["generation"] != report["backend_generation"] or report["generation"] != "tested" or report["mode"] != report["backend_mode"] or report["mode"] != "tested-config" or report["release_id"] != identity or report["backend_release_id"] != identity or report["binding_transport"] != "https":
        raise ValueError("frontend did not call candidate API with destination configuration")
    base.require_proofs(report, ("destination_secret_matches", "backend_destination_secret_matches"), errors, identity)


def validate_observation(name, record):
    errors = []
    try:
        if not isinstance(record, dict) or type(record.get("schema_version")) is not int or record["schema_version"] != 1 or record.get("scenario") != SCENARIOS[name]:
            raise ValueError("invalid coordinated observation")
        base.require_proofs(record, ("cleanup", "worker_dormant", "migration_restart", "destination_only", "schema_preserved", "terminal_restart"), errors, name)
        values = [record[field] for field in ("promotion_id", "task_id", "run_id", "baseline_graph", "migration_owner", "worker_app")]
        if not all(base.valid_uuid(value) for value in values) or len(set(values)) != len(values):
            raise ValueError("invalid or reused migration identity")
        if any(type(record.get(field)) is not int or record[field] != 1 for field in ("executions", "task_count", "attempts")):
            raise ValueError("SQL replay or missing execution")
        identity, baseline = record["promotion_id"], record["baseline_graph"]
        resolving = SCENARIOS[name] == "coordinated_resolve"
        if resolving:
            if record["exit_code"] is not None:
                raise ValueError("uncertain SQL result was rewritten")
            r = record["recovery"]
            base.require_proofs(r, ("session_gone", "receipt_preserved", "admission_disabled"), errors, name)
            if not base.valid_uuid(r["repair_promotion"]) or r["repair_promotion"] in values or r["task_id"] != record["task_id"] or not base.valid_uuid(r["runtime_id"]) or r["status"] != "resolved":
                raise ValueError("unattributed replacement or teardown")
            finish(r["repair"], r["repair_promotion"], baseline, False, errors)
            requested, destroyed, resolved = (base.timestamp(r[field]) for field in ("requested_at", "destroyed_at", "resolved_at"))
            proof = r["worker_proof"]
            worker, health = proof["worker"], proof["health"]
            if not requested <= destroyed <= resolved or base.timestamp(proof["window_start"]) != requested or not requested < base.timestamp(proof["checked_at"]) <= resolved:
                raise ValueError("missing fresh teardown and worker windows")
            if worker["app_id"] != record["worker_app"] or worker["deployment_id"] != r["repair"]["worker_deployment"] or worker["previous_deployment_id"] != worker["deployment_id"] or not base.valid_uuid(worker["instance_id"]) or health["status"] != "healthy":
                raise ValueError("wrong replacement worker proof")
            for sample in (health["baseline"], health["candidate"]):
                if not integer(sample, "attempts", 5) or type(sample["incomplete"]) is not int or sample["incomplete"] != 0 or any(type(sample[k]) is not int or sample[k] < 0 for k in ("failures", "retries", "duration_ms", "queue_delay_ms")) or sample["failures"] != 0 or sample["retries"] != 0:
                    raise ValueError("missing healthy attributed deliveries")
        else:
            if type(record["exit_code"]) is not int or record["exit_code"] != 0:
                raise ValueError("missing successful SQL receipt")
            finish(record, identity, baseline, SCENARIOS[name] == "coordinated_rollback", errors)
            if type(record["retry_attempts"]) is not int or record["retry_attempts"] != 2 or record["retry_release"] != baseline or not base.valid_uuid(record["retry_deployment"]) or record["retry_deployment"] == record["worker_deployment"]:
                raise ValueError("lost cross-cutover job pin")
    except (KeyError, TypeError, ValueError, OverflowError):
        errors.append(f"{name}: inconsistent coordinated execution, cutover or recovery evidence")
    return errors


base.SCENARIOS = SCENARIOS
base.REQUIRED_TESTS = TESTS
base.GROUPS = {GROUP: TESTS}
base.validate_observation = validate_observation


def qualify(log, sha):
    report = base.qualify(log, sha, GROUP)
    starts = re.findall(r"^=== RUN   (\w+)$", log, re.MULTILINE)
    if len(starts) != len(TESTS) or set(starts) != TESTS:
        report["errors"].append("supplemental run must contain its exact four tests")
    records = list(report["scenarios"].values())
    for field in ("promotion_id", "task_id", "run_id"):
        values = [r.get(field) for r in records if isinstance(r, dict)]
        if len({str(value) for value in values}) != len(values):
            report["errors"].append(f"coordinated scenarios reused {field}")
    report["qualified"] = not report["errors"]
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--log", required=True, type=Path)
    parser.add_argument("--sha", required=True)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    try:
        report = qualify(args.log.read_bytes().decode("utf-8"), args.sha)
    except (OSError, UnicodeError) as exc:
        report = {"schema_version": 1, "source_sha": args.sha, "qualified": False, "errors": [str(exc)]}
    args.output.write_text(json.dumps(report, indent=2) + "\n")
    return 0 if report["qualified"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
