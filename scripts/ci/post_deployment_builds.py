"""Closed, bounded projection of advisory build evidence for SSH collection.

The Go verifier owns qualification. Invalid optional data never replaces its
health result; only reviewed build fields reach artifacts or job summaries.
"""

import datetime as dt
import json
import re

import post_deployment_diagnostics as diagnostics
import post_deployment_readiness as readiness
import post_deployment_process as process
import post_deployment_loops as loops
import post_deployment_builder as builder
import post_deployment_handoff as handoff
import post_deployment_preparation as preparation
import post_deployment_snapshot_prime as snapshot_prime
import post_deployment_app_task as app_task
import post_deployment_command_cron as command_cron
import post_deployment_command_cron_outcomes as command_cron_outcomes
import post_deployment_http_cron_delivery as http_cron_delivery
import post_deployment_delayed_task_dispatch as delayed_task_dispatch
import post_deployment_command_cron_timing as command_cron_timing
import post_deployment_app_task_results as app_task_results
import post_deployment_build_history_codec as history_codec
import post_deployment_app_task_operations as app_task_operations
import post_deployment_post_capture as post_capture
import post_deployment_build_recovery as build_recovery


ROLES = ("single-box-v1", "control-plane-v1", "single-box-v2", "control-plane-v2")
# Mirrors of existing pkg/api/limits.go policy, pinned by transport tests.
BUILD_MAX_BYTES = 64 << 10
JOBS_MAX = 8
PREVIEW_MAX = 32
NODES_MAX = 1000
SAMPLES_MAX = 4096
VERSION_MAX_BYTES = 128
NODE_MAX_BYTES = 128
MAX_AGE_SECONDS = 120
CLOCK_SKEW_SECONDS = 30
HISTORY_EVENTS_MAX = 16
HISTORY_BYTES_MAX = 16 << 10
HISTORY_SCOPE = "observed_build_state_and_coverage_counts"
PROGRESS_CLOCKS = ("first_observed_at", "last_observed_at", "first_mismatch_at", "last_mismatch_at",
                   "first_matching_at", "last_matching_at", "last_recovery_at", "last_regression_at")

COUNTS = ("expected", "observed", "matching", "mismatched", "missing", "stale",
          "unavailable", "unexpected", "distinct_revisions")
BASE_JOBS = (("apid", "apid", False), ("gatewayd", "gatewayd-public", False),
             ("schedd", "schedd", False))
JOBS = {
    "single-box-v1": BASE_JOBS + (("vmmd", "vmmd", False),),
    "control-plane-v1": BASE_JOBS + (("gatewayd-internal", "gatewayd-internal", True),
                                    ("schedd-compute", "schedd", True), ("vmmd", "vmmd", True)),
}
JOBS["single-box-v2"] = JOBS["single-box-v1"] + (("builderd", "builderd", False), ("imaged", "imaged", False))
JOBS["control-plane-v2"] = JOBS["control-plane-v1"] + (("builderd", "builderd", True), ("imaged", "imaged", True))
LIMITATIONS = [
    "advisory_not_deployment_qualification", "selected_critical_jobs_not_complete_fleet",
    "explicit_role_not_deployment_intent", "static_targets_are_scrape_inventory_with_minimum_one",
    "compute_membership_from_active_configured_registry", "sampled_metrics_not_binary_attestation",
    "target_addresses_and_raw_labels_excluded", "counts_cover_full_inspected_set_preview_bounded",
]
SOURCE_REASONS = {
    "source_" + reason for reason in (
        "invalid_url", "cancelled", "timeout", "transport_unavailable", "unauthorized", "forbidden",
        "endpoint_not_found", "rate_limited", "runtime_busy", "redirect_rejected", "server_error", "http_error",
        "response_read_failed", "response_too_large", "invalid_response", "unavailable", "stale",
    )
}
ITEM_REASONS = {
    "source_unavailable", "invalid_evidence", "conflicting_targets", "expected_target_missing",
    "invalid_sample_time", "scrape_stale", "scrape_before_boundary", "scrape_down",
    "build_info_missing", "conflicting_build_info", "build_info_stale", "build_info_before_boundary",
    "revision_unavailable",
}


def require(condition):
    if not condition:
        raise ValueError("invalid advisory evidence")


def integer(value, maximum):
    require(type(value) is int and 0 <= value <= maximum)
    return value


def timestamp(value):
    require(isinstance(value, str) and re.fullmatch(
        r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})", value))
    parsed = dt.datetime.fromisoformat(value.replace("Z", "+00:00")).astimezone(dt.timezone.utc)
    require(parsed != dt.datetime.min.replace(tzinfo=dt.timezone.utc))
    return parsed


def unknown_builds(reason):
    return {"advisory": True, "state": "unknown", "reason": reason}


def project_job(raw, spec):
    require(isinstance(raw, dict))
    job, daemon, discovery = spec
    source = "active_configured_compute_registry" if discovery else "scrape_targets_minimum"
    require(raw.get("job") == job and raw.get("daemon") == daemon and raw.get("inventory_source") == source)
    expected = integer(raw.get("expected"), NODES_MAX)
    require(discovery or expected == 1)
    state, reason = raw.get("state"), raw.get("reason", "")
    require(state in ("available", "unknown") and reason in ("", "inventory_unavailable", "source_unavailable", "invalid_evidence"))
    require((state == "available") == (reason == ""))
    out = {"job": job, "daemon": daemon, "inventory_source": source, "state": state, "expected": expected}
    if "reason" in raw:
        out["reason"] = reason
    return out


def project_item(raw, specs, release, generated, after):
    require(isinstance(raw, dict))
    job, daemon = raw.get("job"), raw.get("daemon")
    require(any(job == spec[0] and daemon == spec[1] for spec in specs))
    state, reason = raw.get("state"), raw.get("reason", "")
    out = {"job": job, "daemon": daemon, "state": state}
    if "reason" in raw:
        out["reason"] = reason
    for field, pattern, maximum in (
        ("node", r"[A-Za-z0-9][A-Za-z0-9_.-]*", NODE_MAX_BYTES),
        ("target_id", r"[0-9a-f]{64}", 64),
        ("git_sha", r"[0-9a-f]{40}", 40),
        ("version", r"[A-Za-z0-9][A-Za-z0-9_.+\-]*", VERSION_MAX_BYTES),
    ):
        if field in raw:
            value = raw[field]
            require(isinstance(value, str) and len(value.encode()) <= maximum and (value == "" or re.fullmatch(pattern, value)))
            out[field] = value
    if "unexpected" in raw:
        require(type(raw["unexpected"]) is bool)
        out["unexpected"] = raw["unexpected"]
    observed = {}
    skew = dt.timedelta(seconds=CLOCK_SKEW_SECONDS)
    for field in ("observed_at", "scrape_observed_at"):
        if raw.get(field) is not None:
            at = timestamp(raw[field])
            require(at <= generated + skew)
            observed[field], out[field] = at, raw[field]
    if state in ("matching", "mismatched"):
        sha = out.get("git_sha", "")
        require(re.fullmatch(r"[0-9a-f]{40}", sha) and bool(out.get("target_id")))
        require((state == "matching") == (sha == release))
        require(reason == ("" if state == "matching" else "requested_revision_mismatch"))
        require(len(observed) == 2 and all(at >= generated - dt.timedelta(seconds=MAX_AGE_SECONDS)
                                          and at >= after - skew for at in observed.values()))
    else:
        require(state in ("missing", "stale", "unavailable") and isinstance(reason, str) and reason in ITEM_REASONS)
        require(not out.get("git_sha") and not out.get("version"))
    return out


def evidence_state(counts, jobs):
    if counts["distinct_revisions"] > 1:
        return "mixed", "multiple_observed_revisions"
    if counts["mismatched"]:
        return "mismatched", "requested_revision_mismatch"
    if any(job["state"] != "available" for job in jobs) or any(counts[key] for key in ("missing", "stale", "unavailable", "unexpected")):
        return "incomplete", "incomplete_identity_evidence"
    if counts["matching"]:
        return "matching", ""
    return "unknown", "identity_unavailable"


def project_evidence(raw, role, release, after, now):
    require(isinstance(raw, dict) and role in JOBS)
    # Include unknown fields in the input size check, then exclude them from
    # the projection. This bounds work and prevents new raw properties leaking.
    require(len(json.dumps(raw, allow_nan=False, indent=2).encode()) < BUILD_MAX_BYTES)
    require(raw.get("scope") == "selected_critical_daemon_builds" and raw.get("expected_role") == role)
    require(raw.get("requested_release") == release and timestamp(raw.get("evidence_after")) == timestamp(after))
    require(type(raw.get("max_age_seconds")) is int and raw["max_age_seconds"] == MAX_AGE_SECONDS)
    require(raw.get("limitations") == LIMITATIONS)
    generated, boundary = timestamp(raw.get("generated_at")), timestamp(after)
    skew = dt.timedelta(seconds=CLOCK_SKEW_SECONDS)
    require(now - dt.timedelta(seconds=MAX_AGE_SECONDS) <= generated <= now + skew and generated >= boundary - skew)
    specs = JOBS[role]
    require(isinstance(raw.get("jobs"), list) and len(raw["jobs"]) == len(specs) <= JOBS_MAX)
    jobs = [project_job(job, spec) for job, spec in zip(raw["jobs"], specs)]
    require(isinstance(raw.get("items"), list) and len(raw["items"]) <= PREVIEW_MAX)
    items = [project_item(item, specs, release, generated, boundary) for item in raw["items"]]
    require(isinstance(raw.get("counts"), dict))
    maximum = len(specs) * (NODES_MAX + SAMPLES_MAX)
    counts = {name: integer(raw["counts"].get(name), maximum) for name in COUNTS}
    omitted = integer(raw.get("omitted_items"), maximum)
    total = sum(counts[name] for name in ("matching", "mismatched", "missing", "stale", "unavailable"))
    fresh = counts["matching"] + counts["mismatched"]
    require(counts["expected"] == sum(job["expected"] for job in jobs) and total == len(items) + omitted)
    require(fresh <= counts["observed"] <= total and counts["unexpected"] <= counts["observed"])
    require((counts["distinct_revisions"] == 0) == (fresh == 0))
    require(counts["distinct_revisions"] <= counts["mismatched"] + int(counts["matching"] > 0))
    require(not (counts["matching"] and counts["mismatched"]) or counts["distinct_revisions"] >= 2)
    for state in ("matching", "mismatched", "missing", "stale", "unavailable"):
        require(sum(item["state"] == state for item in items) <= counts[state])
    require(sum(bool(item.get("target_id")) for item in items) <= counts["observed"])
    require(sum(item.get("unexpected", False) for item in items) <= counts["unexpected"])
    state, reason = evidence_state(counts, jobs)
    require(raw.get("state") == state and raw.get("reason", "") == reason)
    out = {"generated_at": raw["generated_at"], "scope": raw["scope"], "expected_role": role,
           "requested_release": release, "evidence_after": raw["evidence_after"], "state": state,
           "max_age_seconds": MAX_AGE_SECONDS, "counts": counts, "jobs": jobs, "items": items,
           "omitted_items": omitted, "limitations": list(LIMITATIONS)}
    if "reason" in raw:
        out["reason"] = reason
    if "readiness" in raw:
        try:
            projected, preview = readiness.project(raw["readiness"], out, raw["items"], generated, boundary)
            out["readiness"] = projected
            for item, ready in zip(items, preview):
                item["readiness"] = ready
        except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
            out["readiness_unavailable_reason"] = "invalid_readiness"
    if "process" in raw:
        try:
            projected, preview = process.project(raw["process"], out, raw["items"], generated, boundary)
            out["process"] = projected
            for item, sample in zip(items, preview):
                item["process"] = sample
        except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
            out["process_unavailable_reason"] = "invalid_process"
    return out


def project_runtime_builds(raw, role, release, after, attempts=None, started_at=None, diagnostic_selection=None):
    """Return owned, reviewed data or a fixed unknown advisory diagnostic."""
    if raw is None:
        return {**unknown_builds("evidence_not_returned" if role else "expected_role_not_selected"), **diagnostics.envelope({},role,diagnostic_selection)}
    try:
        require(isinstance(raw, dict) and raw.get("advisory") is True)
        state, reason = raw.get("state"), raw.get("reason", "")
        out = {"advisory": True, "state": state}
        out.update(diagnostics.envelope(raw,role,diagnostic_selection))
        if "reason" in raw:
            out["reason"] = reason
        if "source_http_status" in raw:
            status = integer(raw["source_http_status"], 999)
            require(status >= 100)
            out["source_http_status"] = status
        if raw.get("evidence") is None:
            allowed = SOURCE_REASONS | {"source_invalid_response", "awaiting_evidence"}
            if not role:
                allowed = {"expected_role_not_selected"}
            require(state == "unknown" and isinstance(reason, str) and reason in allowed)
            if reason == "source_runtime_busy":
                require(out.get("source_http_status") == 429)
            require(integer(raw.get("envelope_omitted_items", 0), PREVIEW_MAX) == 0)
        else:
            evidence = project_evidence(raw["evidence"], role, release, after, dt.datetime.now(dt.timezone.utc))
            require(state == evidence["state"] and reason == evidence.get("reason", ""))
            require("source_http_status" not in out or out["source_http_status"] == 200)
            out["evidence"] = evidence
            if "envelope_omitted_items" in raw:
                omitted = integer(raw["envelope_omitted_items"], PREVIEW_MAX)
                require(omitted <= evidence["omitted_items"] and len(evidence["items"]) + omitted <= PREVIEW_MAX)
                out["envelope_omitted_items"] = omitted
        if "build_recovery_health" in raw and diagnostics.selected(out,"build_recovery_health"):
            try:
                require("evidence" in out)
                out["build_recovery_health"] = build_recovery.project(raw["build_recovery_health"], out["evidence"])
                if "build_recovery_envelope_omitted_items" in raw:
                    omitted = integer(raw["build_recovery_envelope_omitted_items"], PREVIEW_MAX)
                    require(omitted <= out["build_recovery_health"]["omitted_items"] and len(out["build_recovery_health"]["items"])+omitted <= PREVIEW_MAX)
                    out["build_recovery_envelope_omitted_items"] = omitted
            except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
                out.pop("build_recovery_health", None)
                out["build_recovery_health_unavailable_reason"] = "invalid_build_recovery_evidence"
        if "app_task_health" in raw and diagnostics.selected(out,"app_task_health"):
            try:
                require("evidence" in out)
                out["app_task_health"] = app_task.project(raw["app_task_health"], out["evidence"])
                if "app_task_envelope_omitted_items" in raw:
                    omitted = integer(raw["app_task_envelope_omitted_items"], PREVIEW_MAX)
                    require(omitted <= out["app_task_health"]["omitted_items"] and len(out["app_task_health"]["items"])+omitted <= PREVIEW_MAX)
                    out["app_task_envelope_omitted_items"] = omitted
            except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
                out.pop("app_task_health", None)
                out["app_task_health_unavailable_reason"] = "invalid_app_task_evidence"
        if "command_cron_health" in raw and diagnostics.selected(out,"command_cron_health"):
            try:
                require("evidence" in out)
                out["command_cron_health"] = command_cron.project(raw["command_cron_health"], out["evidence"])
                if "command_cron_envelope_omitted_items" in raw:
                    omitted = integer(raw["command_cron_envelope_omitted_items"], PREVIEW_MAX)
                    require(omitted <= out["command_cron_health"]["omitted_items"] and len(out["command_cron_health"]["items"])+omitted <= PREVIEW_MAX)
                    out["command_cron_envelope_omitted_items"] = omitted
            except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
                out.pop("command_cron_health", None)
                out["command_cron_health_unavailable_reason"] = "invalid_command_cron_evidence"
        if "command_cron_outcomes" in raw and diagnostics.selected(out,"command_cron_outcomes"):
            try:
                require("evidence" in out)
                out["command_cron_outcomes"] = command_cron_outcomes.project(raw["command_cron_outcomes"], out["evidence"])
                if "command_cron_outcomes_envelope_omitted_items" in raw:
                    omitted = integer(raw["command_cron_outcomes_envelope_omitted_items"], PREVIEW_MAX)
                    require(omitted <= out["command_cron_outcomes"]["omitted_items"] and len(out["command_cron_outcomes"]["items"])+omitted <= PREVIEW_MAX)
                    out["command_cron_outcomes_envelope_omitted_items"] = omitted
            except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
                out.pop("command_cron_outcomes", None)
                out["command_cron_outcomes_unavailable_reason"] = "invalid_cron_outcome_evidence"
        if "http_cron_delivery" in raw and diagnostics.selected(out,"http_cron_delivery"):
            try:
                require("evidence" in out)
                out["http_cron_delivery"] = http_cron_delivery.project(raw["http_cron_delivery"], out["evidence"])
                if "http_cron_delivery_envelope_omitted_items" in raw:
                    omitted = integer(raw["http_cron_delivery_envelope_omitted_items"], PREVIEW_MAX)
                    require(omitted <= out["http_cron_delivery"]["omitted_items"] and len(out["http_cron_delivery"]["items"])+omitted <= PREVIEW_MAX)
                    out["http_cron_delivery_envelope_omitted_items"] = omitted
            except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
                out.pop("http_cron_delivery", None)
                out["http_cron_delivery_unavailable_reason"] = "invalid_http_cron_delivery_evidence"
        if "delayed_task_dispatch" in raw and diagnostics.selected(out,"delayed_task_dispatch"):
            try:
                require("evidence" in out)
                out["delayed_task_dispatch"] = delayed_task_dispatch.project(raw["delayed_task_dispatch"], out["evidence"])
                if "delayed_task_dispatch_envelope_omitted_items" in raw:
                    omitted = integer(raw["delayed_task_dispatch_envelope_omitted_items"], PREVIEW_MAX)
                    require(omitted <= out["delayed_task_dispatch"]["omitted_items"] and len(out["delayed_task_dispatch"]["items"])+omitted <= PREVIEW_MAX)
                    out["delayed_task_dispatch_envelope_omitted_items"] = omitted
            except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
                out.pop("delayed_task_dispatch", None)
                out["delayed_task_dispatch_unavailable_reason"] = "invalid_delayed_task_dispatch_evidence"
        if "command_cron_timing" in raw and diagnostics.selected(out,"command_cron_timing"):
            try:
                require("evidence" in out)
                out["command_cron_timing"] = command_cron_timing.project(raw["command_cron_timing"], out["evidence"])
                if "command_cron_timing_envelope_omitted_items" in raw:
                    omitted = integer(raw["command_cron_timing_envelope_omitted_items"], PREVIEW_MAX)
                    require(omitted <= out["command_cron_timing"]["omitted_items"] and len(out["command_cron_timing"]["items"])+omitted <= PREVIEW_MAX)
                    out["command_cron_timing_envelope_omitted_items"] = omitted
            except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
                out.pop("command_cron_timing", None)
                out["command_cron_timing_unavailable_reason"] = "invalid_cron_timing_evidence"
        if "app_task_results" in raw and diagnostics.selected(out,"app_task_results"):
            try:
                require("evidence" in out)
                out["app_task_results"] = app_task_results.project(raw["app_task_results"], out["evidence"])
                if "app_task_results_envelope_omitted_items" in raw:
                    omitted = integer(raw["app_task_results_envelope_omitted_items"], PREVIEW_MAX)
                    require(omitted <= out["app_task_results"]["omitted_items"] and len(out["app_task_results"]["items"])+omitted <= PREVIEW_MAX)
                    out["app_task_results_envelope_omitted_items"] = omitted
            except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
                out.pop("app_task_results", None)
                out["app_task_results_unavailable_reason"] = "invalid_result_evidence"
        if "app_task_operations" in raw and diagnostics.selected(out,"app_task_operations"):
            try:
                require("evidence" in out)
                out["app_task_operations"] = app_task_operations.project(raw["app_task_operations"], out["evidence"])
                if "app_task_operations_envelope_omitted_items" in raw:
                    omitted = integer(raw["app_task_operations_envelope_omitted_items"], PREVIEW_MAX)
                    require(omitted <= out["app_task_operations"]["omitted_items"] and len(out["app_task_operations"]["items"])+omitted <= PREVIEW_MAX)
                    out["app_task_operations_envelope_omitted_items"] = omitted
            except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
                out.pop("app_task_operations", None)
                out["app_task_operations_unavailable_reason"] = "invalid_operation_evidence"
        if "post_capture_health" in raw and diagnostics.selected(out,"post_capture_health"):
            try:
                require("evidence" in out)
                out["post_capture_health"] = post_capture.project(raw["post_capture_health"], out["evidence"])
                if "post_capture_envelope_omitted_items" in raw:
                    omitted = integer(raw["post_capture_envelope_omitted_items"], PREVIEW_MAX)
                    require(omitted <= out["post_capture_health"]["omitted_items"] and len(out["post_capture_health"]["items"])+omitted <= PREVIEW_MAX)
                    out["post_capture_envelope_omitted_items"] = omitted
            except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
                out.pop("post_capture_health", None)
                out["post_capture_health_unavailable_reason"] = "invalid_post_capture_evidence"
        if "snapshot_prime_health" in raw and diagnostics.selected(out,"snapshot_prime_health"):
            try:
                require("evidence" in out)
                out["snapshot_prime_health"] = snapshot_prime.project(raw["snapshot_prime_health"], out["evidence"])
                if "snapshot_prime_envelope_omitted_items" in raw:
                    omitted = integer(raw["snapshot_prime_envelope_omitted_items"], PREVIEW_MAX)
                    require(omitted <= out["snapshot_prime_health"]["omitted_items"] and len(out["snapshot_prime_health"]["items"])+omitted <= PREVIEW_MAX)
                    out["snapshot_prime_envelope_omitted_items"] = omitted
            except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
                out.pop("snapshot_prime_health", None)
                out["snapshot_prime_health_unavailable_reason"] = "invalid_snapshot_prime_evidence"
        if "preparation_health" in raw and diagnostics.selected(out,"preparation_health"):
            try:
                require("evidence" in out)
                out["preparation_health"] = preparation.project(raw["preparation_health"], out["evidence"])
                if "preparation_envelope_omitted_items" in raw:
                    omitted = integer(raw["preparation_envelope_omitted_items"], PREVIEW_MAX)
                    require(omitted <= out["preparation_health"]["omitted_items"] and len(out["preparation_health"]["items"])+omitted <= PREVIEW_MAX)
                    out["preparation_envelope_omitted_items"] = omitted
            except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
                out.pop("preparation_health", None)
                out["preparation_health_unavailable_reason"] = "invalid_preparation_evidence"
        if "handoff_health" in raw and diagnostics.selected(out,"handoff_health"):
            try:
                require("evidence" in out)
                out["handoff_health"] = handoff.project(raw["handoff_health"], out["evidence"])
                if "handoff_envelope_omitted_items" in raw:
                    omitted = integer(raw["handoff_envelope_omitted_items"], PREVIEW_MAX)
                    require(omitted <= out["handoff_health"]["omitted_items"] and len(out["handoff_health"]["items"])+omitted <= PREVIEW_MAX)
                    out["handoff_envelope_omitted_items"] = omitted
            except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
                out.pop("handoff_health", None)
                out["handoff_health_unavailable_reason"] = "invalid_handoff_evidence"
        if "builder_health" in raw and diagnostics.selected(out,"builder_health"):
            try:
                require("evidence" in out)
                out["builder_health"] = builder.project(raw["builder_health"], out["evidence"])
                if "builder_envelope_omitted_items" in raw:
                    omitted = integer(raw["builder_envelope_omitted_items"], PREVIEW_MAX)
                    require(omitted <= out["builder_health"]["omitted_items"] and len(out["builder_health"]["items"])+omitted <= PREVIEW_MAX)
                    out["builder_envelope_omitted_items"] = omitted
            except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
                out.pop("builder_health", None)
                out["builder_health_unavailable_reason"] = "invalid_builder_evidence"
        if "loop_health" in raw:
            try:
                require("evidence" in out)
                out["loop_health"] = loops.project(raw["loop_health"], out["evidence"])
                if "loop_envelope_omitted_items" in raw:
                    omitted = integer(raw["loop_envelope_omitted_items"], PREVIEW_MAX)
                    require(omitted <= out["loop_health"]["omitted_items"] and len(out["loop_health"]["items"])+omitted <= PREVIEW_MAX)
                    out["loop_envelope_omitted_items"] = omitted
            except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
                out.pop("loop_health", None)
                out["loop_health_unavailable_reason"] = "invalid_loop_evidence"
        if "progress" in raw or "history" in raw:
            try:
                out["progress"], out["history"] = project_build_history(raw, out, role, release, after, attempts, started_at, diagnostic_selection)
            except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
                out["history_unavailable_reason"] = "invalid_history"
        return out
    except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
        return {**unknown_builds("invalid_advisory_evidence"), **diagnostics.envelope(raw if isinstance(raw, dict) else {},role,diagnostic_selection)}


def build_history_bytes(events):
    body = json.dumps(events, allow_nan=False, indent=2)
    return len(("\n".join("      " + line for line in body.splitlines()) + "\n").encode())


def project_build_progress(raw, current, attempts, started_at):
    require(isinstance(raw, dict))
    attempts = integer(attempts, (1 << 63)-1)
    observations = integer(raw.get("observations"), attempts)
    require(observations == attempts)
    out = {"observations": observations, "regressions": integer(raw.get("regressions"), observations)}
    for name in ("matching_throughout_observed_polls", "observed_incomplete_evidence", "observed_source_unavailable"):
        require(type(raw.get(name)) is bool)
        out[name] = raw[name]
    times = {}
    for name in PROGRESS_CLOCKS:
        if name in raw:
            times[name], out[name] = timestamp(raw[name]), raw[name]
    if observations == 0:
        require(not times and not out["regressions"] and not any(out[name] for name in out if type(out[name]) is bool))
        require(current["state"] == "unknown" and "evidence" not in current)
        return out, times
    start = timestamp(started_at)
    require("first_observed_at" in times and "last_observed_at" in times)
    first, last = times["first_observed_at"], times["last_observed_at"]
    require(start - dt.timedelta(seconds=CLOCK_SKEW_SECONDS) <= first <= last <= dt.datetime.now(dt.timezone.utc) + dt.timedelta(seconds=CLOCK_SKEW_SECONDS))
    require(all(first <= at <= last for at in times.values()))
    for first_key, last_key in (("first_mismatch_at", "last_mismatch_at"), ("first_matching_at", "last_matching_at")):
        require((first_key in times) == (last_key in times))
        if first_key in times:
            require(times[first_key] <= times[last_key])
    if "last_recovery_at" in times:
        require("first_mismatch_at" in times and "first_matching_at" in times)
        require(max(times["first_mismatch_at"], times["first_matching_at"]) <= times["last_recovery_at"] <= times["last_matching_at"])
    require(bool(out["regressions"]) == ("last_regression_at" in times))
    if out["regressions"]:
        require("first_matching_at" in times and "last_mismatch_at" in times)
        require(times["first_matching_at"] <= times["last_regression_at"] <= times["last_mismatch_at"])
    if current["state"] == "matching":
        require(times.get("last_matching_at") == last)
    elif current["state"] in ("mixed", "mismatched"):
        require(times.get("last_mismatch_at") == last)
    else:
        require(not out["matching_throughout_observed_polls"])
    if out["observed_source_unavailable"]:
        require(out["observed_incomplete_evidence"])
    evidence = current.get("evidence")
    unavailable = evidence is None or any(job["state"] != "available" for job in evidence["jobs"])
    incomplete = unavailable or current["state"] in ("unknown", "incomplete") or any((evidence or {}).get("counts", {}).get(name, 0) for name in ("missing", "stale", "unavailable", "unexpected"))
    require(not unavailable or out["observed_source_unavailable"])
    require(not incomplete or out["observed_incomplete_evidence"])
    if out["matching_throughout_observed_polls"]:
        require(times.get("first_matching_at") == first and times.get("last_matching_at") == last)
        require("first_mismatch_at" not in times and not out["observed_incomplete_evidence"] and not out["observed_source_unavailable"])
    return out, times


def project_build_event(raw, role, release, after, times, observations):
    require(isinstance(raw, dict))
    original = raw
    raw = history_codec.expand(raw)
    at = timestamp(raw.get("at"))
    require(times["first_observed_at"] <= at <= times["last_observed_at"])
    observation = integer(raw.get("observation"), observations)
    require(observation > 0)
    state, reason = raw.get("state"), raw.get("reason", "")
    out = {"at": raw["at"], "observation": observation, "state": state}
    out.update(diagnostics.envelope(raw,role,strict=True))
    if "reason" in raw:
        out["reason"] = reason
    if "source_http_status" in raw:
        out["source_http_status"] = integer(raw["source_http_status"], 999)
        require(out["source_http_status"] >= 100)
    if "counts" in raw or "jobs" in raw or "evidence_generated_at" in raw:
        generated = timestamp(raw.get("evidence_generated_at"))
        require(generated <= at + dt.timedelta(seconds=CLOCK_SKEW_SECONDS))
        # Historical evidence keeps its original clock. Do not reclassify an
        # earlier read against collection time or a clamped coordinator clock.
        total = sum(integer(raw.get("counts", {}).get(key), len(JOBS[role]) * (NODES_MAX + SAMPLES_MAX))
                    for key in ("matching", "mismatched", "missing", "stale", "unavailable"))
        synthetic = {"generated_at": raw["evidence_generated_at"], "scope": "selected_critical_daemon_builds",
                     "expected_role": role, "requested_release": release, "evidence_after": after,
                     "state": state, "max_age_seconds": MAX_AGE_SECONDS, "counts": raw.get("counts"),
                     "jobs": raw.get("jobs"), "items": [], "omitted_items": total, "limitations": LIMITATIONS}
        if "reason" in raw:
            synthetic["reason"] = reason
        if "readiness" in raw:
            synthetic["readiness"] = raw["readiness"]
        if "process" in raw:
            synthetic["process"] = raw["process"]
        evidence = project_evidence(synthetic, role, release, after, generated)
        require("source_http_status" not in out or out["source_http_status"] == 200)
        out.update(evidence_generated_at=raw["evidence_generated_at"], counts=evidence["counts"], jobs=evidence["jobs"])
        if "build_recovery_health" in raw and diagnostics.selected(out,"build_recovery_health"):
            try:
                out["build_recovery_health"] = build_recovery.project_summary(raw["build_recovery_health"], evidence)
            except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
                pass
        if "app_task_health" in raw and diagnostics.selected(out,"app_task_health"):
            try:
                out["app_task_health"] = app_task.project_summary(raw["app_task_health"], evidence)
            except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
                pass
        if "command_cron_health" in raw and diagnostics.selected(out,"command_cron_health"):
            try:
                out["command_cron_health"] = command_cron.project_summary(raw["command_cron_health"], evidence)
            except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
                pass
        if "command_cron_outcomes" in raw and diagnostics.selected(out,"command_cron_outcomes"):
            try:
                out["command_cron_outcomes"] = command_cron_outcomes.project_summary(raw["command_cron_outcomes"], evidence)
            except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
                pass
        if "http_cron_delivery" in raw and diagnostics.selected(out,"http_cron_delivery"):
            try:
                out["http_cron_delivery"] = http_cron_delivery.project_summary(raw["http_cron_delivery"], evidence)
            except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
                pass
        if "delayed_task_dispatch" in raw and diagnostics.selected(out,"delayed_task_dispatch"):
            try:
                out["delayed_task_dispatch"] = delayed_task_dispatch.project_summary(raw["delayed_task_dispatch"], evidence)
            except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
                pass
        if "command_cron_timing" in raw and diagnostics.selected(out,"command_cron_timing"):
            try:
                out["command_cron_timing"] = command_cron_timing.project_summary(raw["command_cron_timing"], evidence)
            except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
                pass
        if "app_task_results" in raw and diagnostics.selected(out,"app_task_results"):
            try:
                out["app_task_results"] = app_task_results.project_summary(raw["app_task_results"], evidence)
            except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
                pass
        if "app_task_operations" in raw and diagnostics.selected(out,"app_task_operations"):
            try:
                out["app_task_operations"] = app_task_operations.project_summary(raw["app_task_operations"], evidence)
            except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
                pass
        if "post_capture_health" in raw and diagnostics.selected(out,"post_capture_health"):
            try:
                out["post_capture_health"] = post_capture.project_summary(raw["post_capture_health"], evidence)
            except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
                pass
        if "snapshot_prime_health" in raw and diagnostics.selected(out,"snapshot_prime_health"):
            try:
                out["snapshot_prime_health"] = snapshot_prime.project_summary(raw["snapshot_prime_health"], evidence)
            except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
                pass
        if "preparation_health" in raw and diagnostics.selected(out,"preparation_health"):
            try:
                out["preparation_health"] = preparation.project_summary(raw["preparation_health"], evidence)
            except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
                pass
        if "handoff_health" in raw and diagnostics.selected(out,"handoff_health"):
            try:
                out["handoff_health"] = handoff.project_summary(raw["handoff_health"], evidence)
            except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
                pass
        if "builder_health" in raw and diagnostics.selected(out,"builder_health"):
            try:
                out["builder_health"] = builder.project_summary(raw["builder_health"], evidence)
            except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
                pass
        if "loop_health" in raw:
            try:
                out["loop_health"] = loops.project_summary(raw["loop_health"], evidence)
            except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
                pass

        if "readiness" in evidence:
            out["readiness"] = evidence["readiness"]
        if "process" in evidence:
            out["process"] = evidence["process"]
    else:
        require(state == "unknown" and isinstance(reason, str) and reason in SOURCE_REASONS | {"source_invalid_response", "awaiting_evidence"})
        if reason == "source_runtime_busy":
            require(out.get("source_http_status") == 429)
    return history_codec.project_encoding(out, original)


def project_build_history(raw, current, role, release, after, attempts, started_at, diagnostic_selection=None):
    require(role in JOBS and isinstance(raw.get("history"), dict))
    progress, times = project_build_progress(raw.get("progress"), current, attempts, started_at)
    history = raw["history"]
    require(history.get("scope") == HISTORY_SCOPE)
    require(type(history.get("event_limit")) is int and history["event_limit"] == HISTORY_EVENTS_MAX)
    require(type(history.get("byte_limit")) is int and history["byte_limit"] == HISTORY_BYTES_MAX)
    require(isinstance(history.get("events"), list) and len(history["events"]) <= HISTORY_EVENTS_MAX)
    require(build_history_bytes(history["events"]) <= HISTORY_BYTES_MAX)
    counters = {name: integer(history.get(name), progress["observations"])
                for name in ("total_events", "retained_events", "dropped_events", "envelope_omitted_events")}
    require(counters["envelope_omitted_events"] <= HISTORY_EVENTS_MAX)
    require(counters["retained_events"] == len(history["events"]))
    require(counters["total_events"] == sum(counters[name] for name in ("retained_events", "dropped_events", "envelope_omitted_events")))
    require((counters["total_events"] == 0) == (progress["observations"] == 0))
    event_truncation = counters["dropped_events"] > 0 or counters["envelope_omitted_events"] > 0
    detail_omissions = sum(bool(event.get("omitted_sections")) for event in history["events"])
    require(integer(history.get("detail_omitted_events", 0), HISTORY_EVENTS_MAX) == detail_omissions)
    truncated = event_truncation or detail_omissions > 0
    require(type(history.get("truncated")) is bool and history["truncated"] == truncated)
    events = [project_build_event(event, role, release, after, times, progress["observations"]) for event in history["events"]]
    views = [history_codec.expand(event) for event in events]
    if diagnostic_selection is False:
        require(all("diagnostics" not in event for event in views))
    elif diagnostic_selection is not None:
        require(all(event.get("diagnostics") == diagnostic_selection for event in views))
    previous_at, previous_observation = None, 0
    for i, event in enumerate(views):
        at = timestamp(event["at"])
        require(event["observation"] > previous_observation and (previous_at is None or previous_at <= at))
        require(event["observation"] >= counters["dropped_events"] + counters["envelope_omitted_events"] + i + 1)
        previous_at, previous_observation = at, event["observation"]
        if event["state"] in ("mixed", "mismatched"):
            require("first_mismatch_at" in times and times["first_mismatch_at"] <= at <= times["last_mismatch_at"])
        if event["state"] == "matching":
            require("first_matching_at" in times and times["first_matching_at"] <= at <= times["last_matching_at"])
        unavailable = "counts" not in event or any(job["state"] != "available" for job in event.get("jobs", []))
        incomplete = unavailable or event["state"] in ("unknown", "incomplete") or any(event.get("counts", {}).get(name, 0) for name in ("missing", "stale", "unavailable", "unexpected"))
        require(not unavailable or progress["observed_source_unavailable"])
        require(not incomplete or progress["observed_incomplete_evidence"])
        require(not progress["matching_throughout_observed_polls"] or event["state"] == "matching")
    if events:
        if not event_truncation:
            require(events[0]["observation"] == 1 and timestamp(events[0]["at"]) == times["first_observed_at"])
        last = views[-1]
        require(last["state"] == current["state"] and last.get("reason", "") == current.get("reason", ""))
        require(last.get("source_http_status") == current.get("source_http_status"))
        require(last.get("counts") == current.get("evidence", {}).get("counts"))
        require(last.get("jobs") == current.get("evidence", {}).get("jobs"))
    if "readiness" in raw["progress"]:
        try:
            progress["readiness"] = readiness.project_progress(raw["progress"]["readiness"], progress, times, current, views)
        except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
            progress["readiness_unavailable_reason"] = "invalid_history"
    if "process" in raw["progress"]:
        try:
            progress["process"] = process.project_progress(raw["progress"]["process"], progress, times, current, views, role)
        except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
            progress["process_unavailable_reason"] = "invalid_history"
    if "loop_health" in raw["progress"]:
        try:
            progress["loop_health"] = loops.project_progress(raw["progress"]["loop_health"], progress, times, current, views)
        except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
            progress["loop_health_unavailable_reason"] = "invalid_history"
    if "builder_health" in raw["progress"]:
        try:
            progress["builder_health"] = builder.project_progress(raw["progress"]["builder_health"], progress, times, current, views, role)
        except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
            progress["builder_health_unavailable_reason"] = "invalid_history"
    if "build_recovery_health" in raw["progress"]:
        try:
            progress["build_recovery_health"] = build_recovery.project_progress(raw["progress"]["build_recovery_health"], progress, times, current, views, role)
        except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
            progress["build_recovery_health_unavailable_reason"] = "invalid_history"
    if "app_task_health" in raw["progress"]:
        try:
            progress["app_task_health"] = app_task.project_progress(raw["progress"]["app_task_health"], progress, times, current, views, role)
        except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
            progress["app_task_health_unavailable_reason"] = "invalid_history"
    if "command_cron_health" in raw["progress"]:
        try:
            progress["command_cron_health"] = command_cron.project_progress(raw["progress"]["command_cron_health"], progress, times, current, views, role)
        except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
            progress["command_cron_health_unavailable_reason"] = "invalid_history"
    if "command_cron_outcomes" in raw["progress"]:
        try:
            progress["command_cron_outcomes"] = command_cron_outcomes.project_progress(raw["progress"]["command_cron_outcomes"], progress, times, current, views, role)
        except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
            progress["command_cron_outcomes_unavailable_reason"] = "invalid_history"
    if "http_cron_delivery" in raw["progress"]:
        try:
            progress["http_cron_delivery"] = http_cron_delivery.project_progress(raw["progress"]["http_cron_delivery"], progress, times, current, views, role)
        except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
            progress["http_cron_delivery_unavailable_reason"] = "invalid_history"
    if "delayed_task_dispatch" in raw["progress"]:
        try:
            progress["delayed_task_dispatch"] = delayed_task_dispatch.project_progress(raw["progress"]["delayed_task_dispatch"], progress, times, current, views, role)
        except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
            progress["delayed_task_dispatch_unavailable_reason"] = "invalid_history"
    if "command_cron_timing" in raw["progress"]:
        try:
            progress["command_cron_timing"] = command_cron_timing.project_progress(raw["progress"]["command_cron_timing"], progress, times, current, views, role)
        except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
            progress["command_cron_timing_unavailable_reason"] = "invalid_history"
    if "app_task_results" in raw["progress"]:
        try:
            progress["app_task_results"] = app_task_results.project_progress(raw["progress"]["app_task_results"], progress, times, current, views, role)
        except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
            progress["app_task_results_unavailable_reason"] = "invalid_history"
    if "app_task_operations" in raw["progress"]:
        try:
            progress["app_task_operations"] = app_task_operations.project_progress(raw["progress"]["app_task_operations"], progress, times, current, views, role)
        except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
            progress["app_task_operations_unavailable_reason"] = "invalid_history"
    if "post_capture_health" in raw["progress"]:
        try:
            progress["post_capture_health"] = post_capture.project_progress(raw["progress"]["post_capture_health"], progress, times, current, views, role)
        except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
            progress["post_capture_health_unavailable_reason"] = "invalid_history"
    if "snapshot_prime_health" in raw["progress"]:
        try:
            progress["snapshot_prime_health"] = snapshot_prime.project_progress(raw["progress"]["snapshot_prime_health"], progress, times, current, views, role)
        except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
            progress["snapshot_prime_health_unavailable_reason"] = "invalid_history"
    if "preparation_health" in raw["progress"]:
        try:
            progress["preparation_health"] = preparation.project_progress(raw["progress"]["preparation_health"], progress, times, current, views, role)
        except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
            progress["preparation_health_unavailable_reason"] = "invalid_history"
    if "handoff_health" in raw["progress"]:
        try:
            progress["handoff_health"] = handoff.project_progress(raw["progress"]["handoff_health"], progress, times, current, views, role)
        except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
            progress["handoff_health_unavailable_reason"] = "invalid_history"
    retained_bytes = build_history_bytes(events)
    reported_bytes = integer(history.get("retained_bytes"), HISTORY_BYTES_MAX)
    require(reported_bytes in (retained_bytes, build_history_bytes(history["events"])))
    events, projection_omissions = history_codec.fit_projected_events(events, HISTORY_BYTES_MAX)
    counters["retained_events"] = len(events)
    counters["envelope_omitted_events"] += projection_omissions
    retained_bytes = build_history_bytes(events)
    detail_omissions = sum(bool(e.get("omitted_sections")) for e in events)
    truncated = truncated or projection_omissions > 0 or detail_omissions > 0
    out = {"scope": HISTORY_SCOPE, "event_limit": HISTORY_EVENTS_MAX, "byte_limit": HISTORY_BYTES_MAX,
           **counters, "retained_bytes": retained_bytes, "truncated": truncated, "events": events}
    if "detail_omitted_events" in history or detail_omissions:
        out["detail_omitted_events"] = detail_omissions
    return progress, out


def fit_runtime_builds(report, maximum):
    """Give up advisory detail before rejecting an oversized artifact."""
    builds = report.get("runtime_builds")
    if not builds:
        return
    delayed_task_dispatch.fit(builds, report, maximum)
    http_cron_delivery.fit(builds, report, maximum)
    command_cron_timing.fit(builds, report, maximum)
    command_cron_outcomes.fit(builds, report, maximum)
    app_task_results.fit(builds, report, maximum)
    app_task_operations.fit(builds, report, maximum)
    command_cron.fit(builds, report, maximum)
    app_task.fit(builds, report, maximum)
    build_recovery.fit(builds, report, maximum)
    post_capture.fit(builds, report, maximum)
    snapshot_prime.fit(builds, report, maximum)
    preparation.fit(builds, report, maximum)
    handoff.fit(builds, report, maximum)
    builder.fit(builds, report, maximum)
    loops.fit(builds, report, maximum)
    evidence = builds.get("evidence")
    while evidence and evidence["items"] and len(json.dumps(report, allow_nan=False, indent=2).encode()) + 1 > maximum:
        evidence["items"].pop()
        evidence["omitted_items"] += 1
        builds["envelope_omitted_items"] = builds.get("envelope_omitted_items", 0) + 1
    history = builds.get("history")
    while history and history["events"] and len(json.dumps(report, allow_nan=False, indent=2).encode()) + 1 > maximum:
        history["events"].pop(0)
        history["envelope_omitted_events"] += 1
        history.update(retained_events=len(history["events"]), retained_bytes=build_history_bytes(history["events"]), truncated=True)
        if "detail_omitted_events" in history:
            history["detail_omitted_events"] = sum(bool(e.get("omitted_sections")) for e in history["events"])
    if len(json.dumps(report, allow_nan=False, indent=2).encode()) + 1 > maximum:
        report["runtime_builds"] = {**unknown_builds("report_envelope"),
                                    **{name: builds[name] for name in ("progress", "history", "history_unavailable_reason") if name in builds}}
    if len(json.dumps(report, allow_nan=False, indent=2).encode()) + 1 > maximum:
        report["runtime_builds"] = unknown_builds("report_envelope")
        if history or "progress" in builds:
            report["runtime_builds"]["history_unavailable_reason"] = "report_envelope"


def runtime_build_summary(report):
    builds = report.get("runtime_builds")
    if not builds:
        return ""
    # Only project_runtime_builds output (or local fixed diagnostics) reaches
    # here. Do not interpolate target/node/version strings into Markdown.
    text = "\nRuntime builds (advisory): **" + builds["state"] + "**"
    if builds.get("reason"):
        text += " (" + builds["reason"] + ")"
    if "source_http_status" in builds:
        text += "; source HTTP " + str(builds["source_http_status"])
    text += ".\n\n"
    evidence = builds.get("evidence")
    if evidence:
        text += "Role: `" + evidence["expected_role"] + "`. Counts cover the full inspected set; the artifact contains a bounded preview.\n\n"
        text += ", ".join(name + "=" + str(evidence["counts"][name]) for name in COUNTS) + ".\n\n"
        text += "Preview omitted_items=" + str(evidence["omitted_items"]) + "; envelope_omitted_items=" + str(builds.get("envelope_omitted_items", 0)) + ".\n\n"
        for job in evidence["jobs"]:
            text += "- Build evidence source " + job["job"] + ": " + job["state"]
            if job.get("reason"):
                text += " (" + job["reason"] + ")"
            text += "\n"
    if builds.get("history_unavailable_reason"):
        text += "\nBuild history unavailable (" + builds["history_unavailable_reason"] + ").\n"
    if "progress" in builds:
        progress, history = builds["progress"], builds["history"]
        text += "\nObserved build history: observations=" + str(progress["observations"])
        text += ", earlier_mismatch=" + str("first_mismatch_at" in progress).lower()
        text += ", matching_throughout_observed_polls=" + str(progress["matching_throughout_observed_polls"]).lower()
        text += ", regressions=" + str(progress["regressions"]) + ".\n\n"
        for name in PROGRESS_CLOCKS:
            if name in progress:
                text += "- " + name + ": " + progress[name] + "\n"
        text += "\nBuild history retained_events=" + str(history["retained_events"]) + ", total_events=" + str(history["total_events"])
        text += ", dropped_events=" + str(history["dropped_events"]) + ", envelope_omitted_events=" + str(history["envelope_omitted_events"]) + ".\n"
        compacted = sum("compact_details" in e for e in history["events"])
        if compacted:
            text += "Losslessly compacted history events="+str(compacted)+".\n"
        omitted = sorted({k for e in history["events"] for k in e.get("omitted_sections", ())})
        if omitted:
            text += "History detail omitted for: "+", ".join(omitted)+". Current evidence and progress remain separate.\n"
    text += diagnostics.summary(builds)
    text += readiness.summary(evidence or {}, builds.get("progress"))
    text += process.summary(evidence or {}, builds.get("progress"))
    text += loops.summary(builds)
    text += builder.summary(builds)
    text += handoff.summary(builds)
    text += preparation.summary(builds)
    text += snapshot_prime.summary(builds)
    text += app_task.summary(builds)
    text += command_cron.summary(builds)
    text += command_cron_outcomes.summary(builds)
    text += http_cron_delivery.summary(builds)
    text += delayed_task_dispatch.summary(builds)
    text += command_cron_timing.summary(builds)
    text += app_task_results.summary(builds)
    text += app_task_operations.summary(builds)
    text += post_capture.summary(builds)
    text += build_recovery.summary(builds)
    return text
