#!/usr/bin/env python3
"""Validate supplemental native service-graph evidence; never enables admission."""
import argparse
import importlib.util
import json
from pathlib import Path
import re

spec = importlib.util.spec_from_file_location("promotion_base", Path(__file__).with_name("promotion-qualification.py"))
base = importlib.util.module_from_spec(spec)
spec.loader.exec_module(base)
META = "TestProjectEnvironmentPromotionServiceGraphFixtureMeta"
RUNTIME = "TestProjectEnvironmentPromotionServiceGraphMetal"
GROUP = "promotion-service-graph"
TESTS = {META, RUNTIME}


def report_matches(report, item, graph):
    return (isinstance(report, dict) and
            report.get("deployment_id") == item["frontend_deployment"] and
            report.get("backend_deployment_id") == item["backend_deployment"] and
            base.valid_uuid(report.get("instance_id")) and base.valid_uuid(report.get("backend_instance_id")) and
            report.get("release_id") == report.get("backend_release_id") == graph and
            report.get("generation") == report.get("backend_generation") == "tested" and
            report.get("mode") == report.get("backend_mode") == "tested-config" and
            report.get("destination_secret_matches") is True and
            report.get("backend_destination_secret_matches") is True and report.get("binding_transport") == "https")


def validate_observation(name, record):
    errors, identities, deployments = [], set(), set()
    if not isinstance(record, dict) or type(record.get("schema_version")) is not int or record.get("schema_version") != 1 or record.get("scenario") != "service_graph":
        return ["invalid service-graph observation"]
    base.require_proofs(record, ("cleanup",), errors, "service graph")
    cases = record.get("cases")
    if not isinstance(cases, dict) or set(cases) != {"preflight_refusal", "rollback", "success"}:
        return errors + ["missing exact service-graph case inventory"]
    for case, item in cases.items():
        if not isinstance(item, dict):
            errors.append(f"{case}: invalid case")
            continue
        base.require_proofs(item, ("private_routing", "destination_secrets", "public_baseline", "checkpoint_preserved",
                                   "artifact_reused", "graph_config_consistent"), errors, case)
        try:
            identity = item["promotion_id"]
            if not base.valid_uuid(identity) or identity in identities or item["private_graph"] != identity:
                raise ValueError("reused admission or private graph")
            identities.add(identity)
            if type(item["preflight_generation"]) is not int or item["preflight_generation"] <= 0 or item["preflight_generation"] != item["resumed_generation"] or base.timestamp(item["preflight_deadline"]) != base.timestamp(item["resumed_deadline"]):
                raise ValueError("owner restart reset checkpoint")
            for field in ("frontend_deployment", "backend_deployment"):
                value = item[field]
                if not base.valid_uuid(value) or value in deployments:
                    raise ValueError("missing independent candidate")
                deployments.add(value)
            hashes = [item[field] for field in ("source_config_hash", "destination_config_hash", "baseline_config_hash")]
            if len(set(hashes)) != 3 or not all(isinstance(h, str) and re.fullmatch(r"[a-f0-9]{64}", h) for h in hashes):
                raise ValueError("mixed environment configuration")
            if not base.valid_uuid(item["baseline_graph"]) or not base.valid_uuid(item["selected_graph"]):
                raise ValueError("missing selected graph")
            if not report_matches(item["private_report"], item, identity) or item["private_report"]["instance_id"] != item["initial_frontend_instance"] or item["private_report"]["backend_instance_id"] != item["initial_backend_instance"]:
                raise ValueError("private frontend did not call its candidate API")
            refused, succeeded = case == "preflight_refusal", case == "success"
            if item["phase"] != ("succeeded" if succeeded else "failed") or type(item["publications"]) is not int or item["publications"] != (0 if refused else 1 if succeeded else 2):
                raise ValueError("wrong publication outcome")
            if refused:
                if item["cutover_graph"] != "" or item["selected_graph"] != item["baseline_graph"]:
                    raise ValueError("preflight failure cut over")
            else:
                if not base.valid_uuid(item["cutover_graph"]) or item["cutover_graph"] in (identity, item["baseline_graph"]) or type(item["preflight_rounds"]) is not int or item["preflight_rounds"] < 2 or item["restart_verifying"] is not True:
                    raise ValueError("missing real preflight/restart evidence")
                if succeeded and item["selected_graph"] != item["cutover_graph"] or not succeeded and item["selected_graph"] in (item["baseline_graph"], item["cutover_graph"], identity):
                    raise ValueError("mixed restored graph")
            if not succeeded and (type(item["regressed_rounds"]) is not int or item["regressed_rounds"] < 2):
                raise ValueError("missing real API regression rounds")
            if not succeeded:
                continue
            base.healthy_windows(item, errors, case, item["health_started_at"])
            base.require_proofs(item, ("cold_without_snapshot", "cold_boot_event", "park_wake"), errors, case)
            if item["daemon_restarts"] != ["apid", "vmmd", "gatewayd-internal", "schedd"]:
                raise ValueError("missing exact daemon restart inventory")
            recovered, wake = item["recovered_report"], item["wake_report"]
            if not report_matches(recovered, item, identity) or recovered["backend_instance_id"] == item["initial_backend_instance"]:
                raise ValueError("cold wake selected a sibling API")
            if not base.valid_uuid(item["parked_backend_instance"]) or not report_matches(wake, item, item["cutover_graph"]) or wake["backend_instance_id"] == item["parked_backend_instance"]:
                raise ValueError("park/wake lost selected graph")
            isolation = item["isolation"]
            if not all(base.valid_uuid(isolation[k]) for k in ("owner_account", "foreign_account")) or isolation["owner_account"] == isolation["foreign_account"]:
                raise ValueError("missing foreign account")
            statuses = isolation["statuses"]
            if set(statuses) != {"same_tenant", "foreign_tenant", "candidate_wrong_hint"} or any(type(s) is not int or s not in (403, 404, 409) for s in statuses.values()):
                raise ValueError("forged headers/foreign caller were not denied")
            edge = isolation["edge_statuses"]
            if set(edge) != {"X-Gregale-Release", "Gregale-Target-Deployment", "X-Gregale-Revision"} or any(type(s) is not int or s not in (200, 400, 403, 404, 409, 410) for s in edge.values()):
                raise ValueError("public claims entered a private graph or failed without denial")
        except (KeyError, TypeError, ValueError):
            errors.append(f"{case}: inconsistent routing, recovery or isolation evidence")
    return errors


# Share the base parser's test attribution and source/cleanup boundary. Its
# mandatory six-part inventory remains unchanged in its own module/process.
base.SCENARIOS = {RUNTIME: "service_graph"}
base.REQUIRED_TESTS = TESTS
base.GROUPS = {GROUP: TESTS}
base.validate_observation = validate_observation


def qualify(log, sha):
    report = base.qualify(log, sha, GROUP)
    started = re.findall(r"^=== RUN   (\w+)$", log, re.MULTILINE)
    if len(started) != len(TESTS) or set(started) != TESTS:
        report["errors"].append("supplemental run must contain exactly its two native tests")
        report["qualified"] = False
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
