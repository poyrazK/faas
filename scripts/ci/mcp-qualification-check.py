#!/usr/bin/env python3
"""Validate MCP release evidence; never deploy or contact an external service."""
import argparse
from datetime import datetime
import hashlib
import json
from pathlib import Path
import re
import sys

REQUIRED = {
    "untouched_lockfile_deploy", "native_cold_boot", "native_park_restore",
    "oauth_resource_server", "oauth_provider_lifecycle", "oauth_chatgpt_login", "oauth_codex_login", "official_sdk_interop",
    "tasks_restart_replicas", "tasks_scale_from_zero",
}
NATIVE_PROBE_RUNNER = "gregale-mcp-native-v1"
OAUTH_PROBE_RUNNER = "gregale-mcp-oauth-v1"
OAUTH_LIFECYCLE_RUNNER = "gregale-mcp-oauth-lifecycle-v1"
SDK_INTEROP_RUNNER = "gregale-mcp-sdk-interop-v3"
SDK_INTEROP_VERSION = "2.3.1"
TASKS_RESTART_RUNNER = "gregale-mcp-tasks-restart-v1"
TASKS_SCALE_ZERO_RUNNER = "gregale-mcp-tasks-scale-zero-v2"
TASKS_RESTART_CHECKS = {
    "initial_worker_crash", "two_replica_recovery", "stale_lease_fenced",
    "result_owner_isolation", "final_attempt_input_resume",
}
TASKS_SCALE_ZERO_CHECKS = {
    "observer_app_linked", "observer_app_ready_before", "observer_app_ready_throughout",
    "observer_heartbeat_fresh_before", "observer_heartbeat_fresh_throughout",
    "scale_to_zero_policy", "zero_workers_before",
    "fresh_zero_metric_before", "backlog_metric_observed", "worker_scaled_up",
    "backlog_and_worker_observed_together", "task_completed", "fresh_zero_metric_after",
    "workers_returned_to_zero",
}
OAUTH_CASES = {
    "valid_token": "accepted",
    "expired_token": "rejected",
    "wrong_audience_token": "rejected",
    "insufficient_scope_token": "rejected",
}
NATIVE_WAKE_TIERS = {
    "native_cold_boot": "cold",
    "native_park_restore": "restored",
}


def check_native_probe(path, name, record):
    if record.get("evidence_type") != NATIVE_PROBE_RUNNER:
        return
    try:
        receipt = json.loads(path.read_text(encoding="utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise ValueError(f"{name}: native probe receipt must be valid JSON") from error
    stream = receipt.get("stream") if isinstance(receipt, dict) else None
    checks = receipt.get("checks") if isinstance(receipt, dict) else None
    expected_tier = NATIVE_WAKE_TIERS.get(name)
    if (
        not isinstance(receipt, dict)
        or expected_tier is None
        or receipt.get("version") != 1
        or receipt.get("runner") != NATIVE_PROBE_RUNNER
        or receipt.get("check") != name
        or receipt.get("status") != "passed"
        or receipt.get("target") != record.get("target")
        or receipt.get("expected_wake_tier") != expected_tier
        or receipt.get("observed_wake_tier") != expected_tier
        or receipt.get("park_exit_code") != 0
        or receipt.get("final_park_exit_code") != 0
        or receipt.get("instance_list_exit_code") != 0
        or receipt.get("active_instance_count") != 0
        or receipt.get("no_active_instances") is not True
        or receipt.get("doctor_exit_code") != 0
        or receipt.get("doctor_ok") is not True
        or not isinstance(checks, list)
        or not checks
        or len({check.get("name") for check in checks if isinstance(check, dict)}) != len(checks)
        or any(
            not isinstance(check, dict)
            or check.get("status") != "passed"
            or not isinstance(check.get("name"), str)
            for check in checks
        )
        or not {check.get("name") for check in checks if isinstance(check, dict)}.issuperset(
            {"modern_catalog_discovery", "untrusted_origin", "live_progress"}
        )
        or not isinstance(stream, dict)
        or stream.get("status") != "passed"
        or not isinstance(stream.get("progress_events"), int)
        or stream["progress_events"] < 2
    ):
        raise ValueError(f"{name}: native probe receipt does not prove a successful {expected_tier} wake")


def check_oauth_probe(path, name, record):
    if record.get("evidence_type") != OAUTH_PROBE_RUNNER:
        return
    try:
        receipt = json.loads(path.read_text(encoding="utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise ValueError(f"{name}: OAuth probe receipt must be valid JSON") from error
    if (
        name != "oauth_resource_server"
        or not isinstance(receipt, dict)
        or set(receipt) != {"version", "runner", "check", "status", "target", "app", "observed_at", "cases"}
        or receipt.get("version") != 1
        or receipt.get("runner") != OAUTH_PROBE_RUNNER
        or receipt.get("check") != name
        or receipt.get("status") != "passed"
        or receipt.get("target") != record.get("target")
        or not isinstance(receipt.get("app"), str)
        or not re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", receipt["app"])
        or not isinstance(receipt.get("observed_at"), str)
    ):
        raise ValueError(f"{name}: OAuth probe receipt metadata is invalid")

    cases = receipt.get("cases")
    if not isinstance(cases, list) or len(cases) != len(OAUTH_CASES):
        raise ValueError(f"{name}: OAuth probe receipt must contain every token case exactly once")
    by_name = {case.get("name"): case for case in cases if isinstance(case, dict)}
    if len(by_name) != len(cases) or set(by_name) != set(OAUTH_CASES):
        raise ValueError(f"{name}: OAuth probe receipt token cases are incomplete or duplicated")

    for case_name, expected in OAUTH_CASES.items():
        case = by_name[case_name]
        common = {"name", "expected", "observed", "status", "cli_exit_code"}
        if case_name == "valid_token":
            allowed = common | {"doctor_checks", "progress_events"}
        elif case_name == "insufficient_scope_token":
            allowed = common | {"http_status"}
        else:
            allowed = common | {"doctor_checks", "http_status"}
        if (
            set(case) != allowed
            or case.get("expected") != expected
            or case.get("observed") != expected
            or case.get("status") != "passed"
            or type(case.get("cli_exit_code")) is not int
        ):
            raise ValueError(f"{name}: OAuth probe case {case_name} is invalid")
        if case_name == "valid_token":
            checks = case.get("doctor_checks")
            if (
                case["cli_exit_code"] != 0
                or not isinstance(checks, dict)
                or checks != {
                    "oauth_resource_discovery": "passed",
                    "modern_catalog_discovery": "passed",
                    "live_progress": "passed",
                }
                or type(case.get("progress_events")) is not int
                or case["progress_events"] < 2
            ):
                raise ValueError(f"{name}: valid-token case did not prove OAuth discovery and harmless tool access")
        elif case_name in {"expired_token", "wrong_audience_token"}:
            checks = case.get("doctor_checks")
            if (
                case["cli_exit_code"] == 0
                or case.get("http_status") != 401
                or not isinstance(checks, dict)
                or checks != {
                    "oauth_resource_discovery": "passed",
                    "modern_catalog_discovery": "failed",
                }
            ):
                raise ValueError(f"{name}: {case_name} was not rejected with HTTP 401")
        elif case["cli_exit_code"] == 0 or case.get("http_status") != 403:
            raise ValueError(f"{name}: insufficient-scope token was not rejected with HTTP 403")


def check_oauth_lifecycle(path, name, record):
    if record.get("evidence_type") != OAUTH_LIFECYCLE_RUNNER:
        return
    try:
        receipt = json.loads(path.read_text(encoding="utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise ValueError(f"{name}: OAuth lifecycle receipt must be valid JSON") from error
    if (
        name != "oauth_provider_lifecycle"
        or not isinstance(receipt, dict)
        or set(receipt) != {
            "version", "runner", "check", "status", "target", "app", "issuer",
            "observed_at", "discovery", "authorization", "refresh", "revocation", "key_rotation",
        }
        or receipt.get("version") != 1
        or receipt.get("runner") != OAUTH_LIFECYCLE_RUNNER
        or receipt.get("check") != name
        or receipt.get("status") != "passed"
        or receipt.get("target") != record.get("target")
        or not isinstance(receipt.get("app"), str)
        or not re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", receipt["app"])
        or not isinstance(receipt.get("issuer"), str)
        or not receipt["issuer"].startswith("https://")
        or any(ord(char) < 33 or ord(char) == 127 for char in receipt["issuer"])
        or not isinstance(receipt.get("observed_at"), str)
    ):
        raise ValueError(f"{name}: OAuth lifecycle receipt metadata is invalid")
    discovery = receipt["discovery"]
    authorization = receipt["authorization"]
    refresh = receipt["refresh"]
    revocation = receipt["revocation"]
    rotation = receipt["key_rotation"]
    if (
        not isinstance(discovery, dict)
        or set(discovery) != {"status", "response_type", "pkce_method", "jwks_key_count", "resource_matches", "issuer_advertised"}
        or discovery.get("status") != "passed"
        or discovery.get("response_type") != "code"
        or discovery.get("pkce_method") != "S256"
        or type(discovery.get("jwks_key_count")) is not int
        or discovery["jwks_key_count"] < 1
        or discovery.get("resource_matches") is not True
        or discovery.get("issuer_advertised") is not True
        or not isinstance(authorization, dict)
        or set(authorization) != {"status", "state_validated", "code_exchange", "resource_probe", "progress_events", "consent_prompt_requested"}
        or authorization.get("status") != "passed"
        or authorization.get("state_validated") is not True
        or authorization.get("code_exchange") != "passed"
        or authorization.get("resource_probe") != "passed"
        or type(authorization.get("progress_events")) is not int
        or authorization["progress_events"] < 0
        or authorization.get("consent_prompt_requested") is not True
        or not isinstance(refresh, dict)
        or set(refresh) != {"status", "token_issued", "resource_probe", "rotation"}
        or refresh.get("status") != "passed"
        or refresh.get("token_issued") is not True
        or refresh.get("resource_probe") != "passed"
        or refresh.get("rotation") not in {"rotated", "stable"}
        or not isinstance(revocation, dict)
        or set(revocation) != {"status", "endpoint_response", "refresh_rejected"}
        or revocation.get("status") != "passed"
        or revocation.get("endpoint_response") != 200
        or revocation.get("refresh_rejected") is not True
        or not isinstance(rotation, dict)
        or set(rotation) != {"status", "previous_kid", "current_kid", "jwks_confirmed"}
        or rotation.get("status") not in {"passed", "not_requested"}
    ):
        raise ValueError(f"{name}: OAuth lifecycle receipt does not prove the required provider flow")
    if rotation["status"] == "not_requested":
        if rotation.get("previous_kid") != "" or rotation.get("current_kid") != "" or rotation.get("jwks_confirmed") is not False:
            raise ValueError(f"{name}: unrequested signing-key rotation evidence is inconsistent")
    elif (
        not isinstance(rotation.get("previous_kid"), str)
        or not rotation["previous_kid"]
        or not isinstance(rotation.get("current_kid"), str)
        or not rotation["current_kid"]
        or rotation["current_kid"] == rotation["previous_kid"]
        or rotation.get("jwks_confirmed") is not True
    ):
        raise ValueError(f"{name}: signing-key rotation was not proven")


def check_sdk_interop(path, name, record):
    if record.get("evidence_type") != SDK_INTEROP_RUNNER:
        return
    try:
        receipt = json.loads(path.read_text(encoding="utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise ValueError(f"{name}: SDK interoperability receipt must be valid JSON") from error
    if (
        name != "official_sdk_interop"
        or not isinstance(receipt, dict)
        or set(receipt) != {
            "version", "runner", "check", "status", "target", "observed_at",
            "sdk", "runtimes", "failures",
        }
        or receipt.get("version") != 1
        or receipt.get("runner") != SDK_INTEROP_RUNNER
        or receipt.get("check") != name
        or receipt.get("status") != "passed"
        or receipt.get("target") != record.get("target")
        or not isinstance(receipt.get("observed_at"), str)
        or not isinstance(receipt.get("failures"), list)
        or receipt["failures"]
    ):
        raise ValueError(f"{name}: SDK interoperability receipt metadata is invalid")
    try:
        observed_at = datetime.fromisoformat(receipt["observed_at"].replace("Z", "+00:00"))
    except ValueError as error:
        raise ValueError(f"{name}: SDK interoperability timestamp is invalid") from error
    if observed_at.tzinfo is None:
        raise ValueError(f"{name}: SDK interoperability timestamp must include a timezone")

    sdk = receipt["sdk"]
    if (
        not isinstance(sdk, dict)
        or set(sdk) != {"package", "version"}
        or sdk.get("package") != "@modelcontextprotocol/client"
        or sdk.get("version") != SDK_INTEROP_VERSION
    ):
        raise ValueError(f"{name}: SDK package or pinned version is invalid")

    expected_capabilities = {
        "node": ["completions", "prompts", "resources", "tools"],
        "go": ["completions", "prompts", "resources", "tools"],
        "python": ["completions", "prompts", "resources", "tools"],
    }
    runtimes = receipt["runtimes"]
    if not isinstance(runtimes, list) or len(runtimes) != len(expected_capabilities):
        raise ValueError(f"{name}: receipt must cover the Node, Go and Python starters")
    by_runtime = {item.get("runtime"): item for item in runtimes if isinstance(item, dict)}
    if len(by_runtime) != len(runtimes) or set(by_runtime) != set(expected_capabilities):
        raise ValueError(f"{name}: runtime results are incomplete or duplicated")

    expected_checks = {
        "discovery", "initialize", "tools_list", "tool_calls", "resources",
        "prompts", "completions", "progress", "progress_events",
    }
    for runtime, capabilities in expected_capabilities.items():
        result = by_runtime[runtime]
        if (
            set(result) != {"runtime", "capabilities", "protocols"}
            or result.get("capabilities") != capabilities
            or not isinstance(result.get("protocols"), list)
            or len(result["protocols"]) != 2
        ):
            raise ValueError(f"{name}: {runtime} starter capability receipt is invalid")
        protocols = {item.get("mode"): item for item in result["protocols"] if isinstance(item, dict)}
        if len(protocols) != 2 or set(protocols) != {"modern", "legacy"}:
            raise ValueError(f"{name}: {runtime} must pass modern and legacy protocol modes")
        for mode, protocol_version, era in (
            ("modern", "2026-07-28", "modern"),
            ("legacy", "2025-11-25", "legacy"),
        ):
            protocol = protocols[mode]
            checks = protocol.get("checks")
            expected_discovery = "passed" if mode == "modern" else "not_applicable"
            expected_catalog = "passed"
            expected_progress = "passed"
            if (
                set(protocol) != {
                    "mode", "protocol_era", "protocol_version", "checks",
                    "tool_count", "resource_count", "resource_template_count", "prompt_count", "completion_count",
                }
                or protocol.get("protocol_era") != era
                or protocol.get("protocol_version") != protocol_version
                or not isinstance(checks, dict)
                or set(checks) != expected_checks
                or checks.get("discovery") != expected_discovery
                or checks.get("initialize") != "passed"
                or checks.get("tools_list") != "passed"
                or checks.get("tool_calls") != "passed"
                or checks.get("resources") != expected_catalog
                or checks.get("prompts") != expected_catalog
                or checks.get("completions") != "passed"
                or checks.get("progress") != expected_progress
                or type(checks.get("progress_events")) is not int
                or checks["progress_events"] != 3
                or type(protocol.get("tool_count")) is not int
                or protocol["tool_count"] < 3
                or type(protocol.get("resource_count")) is not int
                or protocol["resource_count"] < 1
                or type(protocol.get("resource_template_count")) is not int
                or protocol["resource_template_count"] < 1
                or type(protocol.get("prompt_count")) is not int
                or protocol["prompt_count"] < 1
                or type(protocol.get("completion_count")) is not int
                or protocol["completion_count"] != 2
            ):
                raise ValueError(f"{name}: {runtime} {mode} protocol result is invalid")


def validate_tasks_restart_receipt(receipt, name, target, require_passed=True):
    if (
        name != "tasks_restart_replicas"
        or not isinstance(receipt, dict)
        or set(receipt) != {
            "version", "runner", "check", "status", "target", "observed_at",
            "checks", "observations", "failures",
        }
        or receipt.get("version") != 1
        or receipt.get("runner") != TASKS_RESTART_RUNNER
        or receipt.get("check") != name
        or receipt.get("status") not in {"passed", "failed"}
        or receipt.get("target") != target
        or not isinstance(receipt.get("observed_at"), str)
        or not isinstance(receipt.get("failures"), list)
    ):
        raise ValueError(f"{name}: task restart receipt metadata is invalid")
    try:
        observed_at = datetime.fromisoformat(receipt["observed_at"].replace("Z", "+00:00"))
    except ValueError as error:
        raise ValueError(f"{name}: task restart receipt timestamp is invalid") from error
    if observed_at.tzinfo is None:
        raise ValueError(f"{name}: task restart receipt timestamp must include a timezone")

    checks = receipt["checks"]
    observations = receipt["observations"]
    if (
        not isinstance(checks, dict)
        or set(checks) != TASKS_RESTART_CHECKS
        or any(status not in {"passed", "failed"} for status in checks.values())
        or not isinstance(observations, dict)
        or set(observations) != {
            "replacement_replicas", "recovery_attempt", "recovered_handler_executions",
            "final_attempt", "resumed_attempt",
        }
        or any(type(value) is not int or value < 0 for value in observations.values())
    ):
        raise ValueError(f"{name}: task restart receipt checks or observations are invalid")
    failures = receipt["failures"]
    if any(
        not isinstance(failure, dict)
        or set(failure) != {"stage", "reason"}
        or not isinstance(failure.get("stage"), str)
        or not re.fullmatch(r"[a-z][a-z0-9_]{0,63}", failure["stage"])
        or failure.get("reason") != "qualification_failed"
        for failure in failures
    ):
        raise ValueError(f"{name}: task restart receipt failure details are invalid")
    if receipt["status"] == "failed":
        if not failures:
            raise ValueError(f"{name}: failed task restart receipt must record a failure stage")
        if require_passed:
            raise ValueError(f"{name}: task restart qualification did not pass")
        return
    expected_observations = {
        "replacement_replicas": 2,
        "recovery_attempt": 2,
        "recovered_handler_executions": 1,
        "final_attempt": 3,
        "resumed_attempt": 3,
    }
    if (
        failures
        or any(status != "passed" for status in checks.values())
        or observations != expected_observations
    ):
        raise ValueError(f"{name}: task restart receipt does not prove crash recovery across two replicas")


def check_tasks_restart_receipt(path, name, record):
    if record.get("evidence_type") != TASKS_RESTART_RUNNER:
        return
    try:
        receipt = json.loads(path.read_text(encoding="utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise ValueError(f"{name}: task restart receipt must be valid JSON") from error
    validate_tasks_restart_receipt(receipt, name, record.get("target"))


def validate_tasks_scale_zero_receipt(receipt, name, target, require_passed=True):
    if (
        name != "tasks_scale_from_zero"
        or not isinstance(receipt, dict)
        or set(receipt) != {
            "version", "runner", "check", "status", "target", "app", "observer_app", "observed_at",
            "checks", "observations", "failures",
        }
        or receipt.get("version") != 2
        or receipt.get("runner") != TASKS_SCALE_ZERO_RUNNER
        or receipt.get("check") != name
        or receipt.get("status") not in {"passed", "failed"}
        or receipt.get("target") != target
        or not isinstance(receipt.get("app"), str)
        or not re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", receipt["app"])
        or not isinstance(receipt.get("observer_app"), str)
        or (receipt["observer_app"] and not re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", receipt["observer_app"]))
        or (receipt["observer_app"] and receipt["observer_app"] == receipt["app"])
        or not isinstance(receipt.get("observed_at"), str)
        or not isinstance(receipt.get("failures"), list)
    ):
        raise ValueError(f"{name}: task scale-from-zero receipt metadata is invalid")
    try:
        observed_at = datetime.fromisoformat(receipt["observed_at"].replace("Z", "+00:00"))
    except ValueError as error:
        raise ValueError(f"{name}: task scale-from-zero timestamp is invalid") from error
    if observed_at.tzinfo is None:
        raise ValueError(f"{name}: task scale-from-zero timestamp must include a timezone")

    checks = receipt["checks"]
    observations = receipt["observations"]
    if (
        not isinstance(checks, dict)
        or set(checks) != TASKS_SCALE_ZERO_CHECKS
        or any(value not in {"passed", "failed"} for value in checks.values())
        or not isinstance(observations, dict)
        or set(observations) != {
            "initial_replicas", "peak_replicas", "peak_outstanding", "final_replicas",
            "freshness_seconds", "completed_tasks", "observer_min_replicas", "observer_initial_replicas",
        }
        or any(type(value) is not int or value < 0 for value in observations.values())
    ):
        raise ValueError(f"{name}: task scale-from-zero checks or observations are invalid")
    failures = receipt["failures"]
    if any(
        not isinstance(failure, dict)
        or set(failure) != {"stage", "reason"}
        or not isinstance(failure.get("stage"), str)
        or not re.fullmatch(r"[a-z][a-z0-9_]{0,63}", failure["stage"])
        or failure.get("reason") != "qualification_failed"
        for failure in failures
    ):
        raise ValueError(f"{name}: task scale-from-zero failure details are invalid")
    if receipt["status"] == "failed":
        if not failures:
            raise ValueError(f"{name}: failed task scale-from-zero receipt must record a failure stage")
        if require_passed:
            raise ValueError(f"{name}: task scale-from-zero qualification did not pass")
        return

    expected = observations
    if (
        failures
        or any(value != "passed" for value in checks.values())
        or not receipt["observer_app"]
        or receipt["observer_app"] == receipt["app"]
        or expected["initial_replicas"] != 0
        or expected["peak_replicas"] < 1
        or expected["peak_outstanding"] < 1
        or expected["final_replicas"] != 0
        or expected["freshness_seconds"] < 1
        or expected["completed_tasks"] != 1
        or expected["observer_min_replicas"] < 1
        or expected["observer_initial_replicas"] < 1
    ):
        raise ValueError(f"{name}: receipt does not prove fresh backlog scaling from zero, task completion, and return to zero")


def check_tasks_scale_zero_receipt(path, name, record):
    if record.get("evidence_type") != TASKS_SCALE_ZERO_RUNNER:
        return
    try:
        receipt = json.loads(path.read_text(encoding="utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise ValueError(f"{name}: task scale-from-zero receipt must be valid JSON") from error
    validate_tasks_scale_zero_receipt(receipt, name, record.get("target"))


def check(path, commit):
    root = path.parent.resolve()
    evidence = json.loads(path.read_text())
    if evidence.get("version") != 1 or evidence.get("commit") != commit:
        raise ValueError("evidence must be version 1 and match the reviewed source commit")
    if not re.fullmatch(r"[0-9a-f]{40}", commit):
        raise ValueError("reviewed source must be a full commit SHA")
    records = evidence.get("checks", [])
    by_name = {record["name"]: record for record in records}
    if len(by_name) != len(records) or set(by_name) != REQUIRED:
        raise ValueError("provide exactly one evidence record for every required qualification")
    for name, record in by_name.items():
        if record.get("status") != "passed" or not record.get("target"):
            raise ValueError(f"{name}: qualification is incomplete or has no test target")
        if name == "tasks_scale_from_zero" and record.get("evidence_type") != TASKS_SCALE_ZERO_RUNNER:
            raise ValueError(f"{name}: validated staging-runner evidence is required")
        relative = Path(record["artifact"])
        artifact = (root / relative).resolve()
        if relative.is_absolute() or not artifact.is_relative_to(root) or not artifact.is_file():
            raise ValueError(f"{name}: artifact must be a file within the evidence directory")
        if not re.fullmatch(r"[0-9a-f]{64}", record.get("sha256", "")):
            raise ValueError(f"{name}: artifact SHA-256 is required")
        if hashlib.sha256(artifact.read_bytes()).hexdigest() != record["sha256"]:
            raise ValueError(f"{name}: artifact digest does not match")
        if record.get("evidence_type") is not None and record.get("evidence_type") not in {
            NATIVE_PROBE_RUNNER, OAUTH_PROBE_RUNNER, OAUTH_LIFECYCLE_RUNNER, SDK_INTEROP_RUNNER,
            TASKS_RESTART_RUNNER, TASKS_SCALE_ZERO_RUNNER,
        }:
            raise ValueError(f"{name}: unsupported evidence type")
        check_native_probe(artifact, name, record)
        check_oauth_probe(artifact, name, record)
        check_oauth_lifecycle(artifact, name, record)
        check_sdk_interop(artifact, name, record)
        check_tasks_restart_receipt(artifact, name, record)
        check_tasks_scale_zero_receipt(artifact, name, record)
    return {"ok": True, "commit": commit, "checks": sorted(by_name)}


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("evidence", type=Path)
    parser.add_argument("--commit", required=True)
    args = parser.parse_args()
    try:
        print(json.dumps(check(args.evidence, args.commit)))
    except (ValueError, KeyError, TypeError, OSError) as error:
        print(json.dumps({"ok": False, "error": str(error)}))
        sys.exit(1)
