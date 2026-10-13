#!/usr/bin/env python3
"""Portable harness tests; these never certify a native fleet."""
import copy
import hashlib
import contextlib
import io
import importlib.util
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch
import uuid
import xml.etree.ElementTree as ET

spec = importlib.util.spec_from_file_location("native", Path(__file__).with_name("scenario-restart-native.py"))
native = importlib.util.module_from_spec(spec)
spec.loader.exec_module(native)


def http_proofs(names):
    return [{"name": name, "passed": True, "status": 200} for name in names]


def receipt(case="success"):
    suite, scenario, mode, sequence = native.case_contract(case)
    steps = [{"name": name, "status": "passed"} for name in sequence]
    fault = {"rules_installed": 1, "matches": [{"matches": 6, "min_matches": 6 if suite == "checkout-batch" else 1, "rule": {"kind": "tcp_timeout"}}]}
    if case == "tcp-flight" or case in native.TRANSPORT_CASES or case in native.STRICT_CASES:
        pass
    elif case in ("auth-stall", "commit-loss", "commit-request", "commit-response", "bandwidth", "read-cut", "ssl-request", "retry-budget", "circuit-breaker", "bulkhead", "combined", "repeated", "isolation"):
        pass
    elif suite in ("http-restart-matrix", "http-dependency-restart"):
        dependency = suite == "http-dependency-restart"
        matrix = native.DEPENDENCY_MATRIX if dependency else native.RESTART_MATRIX
        steps[0]["requests"] = http_proofs(("seed-charge", "seed-ledger"))
        steps[1]["restart"] = {"workload": "payment" if dependency else "app", "status": "passed", "processing_status": "completed", "boot_method": "cold_boot",
            "wake_id": str(uuid.uuid4()), "readiness_attempts": 1, "storage_before": http_proofs(("retained-ledger",)),
            "storage_after": http_proofs(("retained-ledger",)), "readiness": http_proofs(("replacement-payment-ready" if dependency else "replacement-idle",)),
            "in_flight": {"name": "interrupted-write", "checkpoint_attempts": 1, "pending_at_restart": True, "restart_completed": True,
                "joined": True, "outcome": "canceled_after_restart", "checkpoints": http_proofs(("caller-pending", "provider-" + matrix[case][0]))}}
        steps[2]["requests"] = http_proofs(("reconciled-ledger",))
    elif suite == "pending-recovery":
        steps[0]["requests"] = http_proofs(("probe-baseline-ready",))
        stuck = mode == "stuck"
        thresholds = [{"metric": metric, "passed": True, "limit": limit, "actual": actual, "operator": operator} for metric, limit, actual, operator in (("p95_ms", 1500, 20, "<="), ("error_rate", 0, 0, "<="), ("min_samples", 3, 5, ">="))]
        recovery = {"max_pending_age_ms": 500, "within_ms": 2000, "sustain_ms": 2000, "window_ms": 1000, "windows_checked": 21,
                    "thresholds": thresholds, "passed": not stuck, "overdue_requests": 1 if stuck else 0,
                    "error": "no sustained healthy interval; 1 requests exceed max_pending_age" if stuck else ""}
        if not stuck:
            recovery["recovery_ms"] = 1000
        phases = [{"name": name, "actual_duration_ms": 2000, "started_at": "2026-01-01T00:00:00Z", "finished_at": "2026-01-01T00:00:02Z", "requests": 10} for name in ("baseline", "dependency-outage", "recovery")]
        phases[1]["chaos"] = fault
        phases[2]["recovery"] = recovery
        steps[1]["load"] = {"status": "failed" if stuck else "passed", "chaos_phases": phases, "streams": [{"name": "healthy"}, {"name": "pending"}], "thresholds": thresholds}
        if stuck:
            steps = steps[:2]
            steps[1].update(status="failed", error='phase "recovery" recovery failed: max_pending_age')
        else:
            steps[2]["requests"] = http_proofs(("dependency-reachable-again",))
    else:
        steps[1]["chaos"] = fault
        steps[0]["load"] = steps[4]["load"] = {"status": "passed"}
        steps[2]["restart"] = {"workload": "app", "status": "passed", "processing_status": "completed", "boot_method": "cold_boot",
                               "wake_id": str(uuid.uuid4()), "readiness_attempts": 1,
                               "storage_before": [{"passed": True}], "storage_after": [{"passed": True}], "readiness": [{"passed": True}]}
        steps[3]["fault_cleared"] = True
        if suite == "checkout-batch":
            pending = tuple("customer-" + str(i) + "-pending" for i in range(6))
            steps[0]["requests"] = http_proofs(("ledger-before-batch",))
            steps[1]["requests"] = http_proofs(("all-customer-intents-persisted", *pending))
            steps[4]["requests"] = http_proofs(("every-order-confirmed", *("customer-" + str(i) + "-confirmed" for i in range(6))))
            for field in ("storage_before", "storage_after"):
                steps[2]["restart"][field] = http_proofs(("preserved-customer-intents", *("preserved-customer-" + str(i) + "-pending" for i in range(6))))
            steps[2]["restart"]["readiness"] = http_proofs(("batch-checkout-ready",))
        if mode != "success":
            steps = steps[:2] + [{"name": "intentional-failure" if mode == "failure" else "await-cancellation", "status": "failed",
                                  "error": "command: node: exit status 42" if mode == "failure" else "command: node: context canceled"}]
    result = [{"scenario": scenario, "engine": "real-vm", "profile": "warm", "run_id": uuid.uuid4().hex,
             "app_slug": "isolated-app", "source_sha256": "a" * 64, "status": "passed" if mode == "success" else "failed",
             "phases": [{"name": "cleanup", "status": "passed"}], "steps": steps,
             "workload_app_ids": {"native-checkout-" + case: str(uuid.uuid4()), "payment": str(uuid.uuid4())}}]
    if case in native.STRICT_CASES:
        result[0]["service_hot"] = {"payment": {"no_new_wake": True, "planned_restarts": []}}
        strict = {"passed": mode == "success", "requests": 30, "correlated": 30, "limit": 2, "capacity": 1024, "overflow": 0, "uncorrelated_attempts": 0, "max_attempts": 2, "violation_count": 0}
        if case == "req-outlier": strict.update(max_attempts=11, violation_count=1)
        if case == "req-no-id": strict.update(uncorrelated_attempts=40, error="dependency calls have missing, invalid or stale correlation IDs")
        if case == "req-no-ack": strict.update(error="incoming request correlation or completion receipt missing")
        steps[0]["requests"] = http_proofs(("ready",))
        steps[1]["status"] = "passed" if mode == "success" else "failed"
        steps[1]["load"] = {"chaos_phases": [{"name": "baseline"}, {"name": "fault", "chaos": fault, "retry_amplification": {"passed": mode == "success", "request": "dependency-read", "max_attempts_per_request": 2, "attempts_per_request": 1.5, "attempts": 45, "per_request": strict}}, {"name": "recovery", "step_recovery": {"dependency-read": {"passed": True}}}]}
        if mode == "success": steps[2]["requests"] = http_proofs(("confirmed",))
    if case == "tcp-flight":
        steps[0]["requests"] = http_proofs(("ready",))
        steps[1].update(status="failed", error="command: node: context canceled", chaos={"rules_installed": 1, "matches": [{"matches": 2, "min_matches": 2, "rule": {"kind": "tcp_timeout"}}]})
    if case in native.TRANSPORT_CASES:
        transport_receipt(result[0], case)
    if suite == "http-dependency-restart":
        result[0]["service_hot"] = {"payment": {"no_new_wake": True, "planned_restarts": [steps[1]["restart"]["wake_id"]]}}
    if case in ("auth-stall", "commit-loss", "commit-request", "commit-response", "bandwidth", "read-cut", "ssl-request", "retry-budget", "circuit-breaker", "bulkhead", "combined", "repeated", "isolation"):
        result[0]["service_hot"] = {"payment": {"no_new_wake": True, "planned_restarts": []}}
        result[0]["requests"] = http_proofs(("caller-baseline" if case.startswith("commit-") else "query-baseline",))
    if case == "isolation":
        result[0]["workload_app_ids"]["inventory"] = str(uuid.uuid4())
        result[0]["service_hot"]["inventory"] = {"no_new_wake": True, "planned_restarts": []}
        result[0]["requests"] = http_proofs(("a-baseline", "b-baseline"))
    return result


def transport_receipt(report, case):
    negative = case in ("pool-queue", "req-body", "conn-gauge")
    steps = report["steps"]
    steps[0]["requests"] = http_proofs(("ready",))
    steps[1]["status"] = "failed" if negative else "passed"
    if negative: steps[1]["error"] = "configured assertion failed"
    elif case == "tcp-cancel": steps[-1].update(status="failed", error="command: node: context canceled")
    else: steps[-1]["requests"] = http_proofs(("dependency-restored",))
    report["service_hot"] = {"payment": {"no_new_wake": True, "planned_restarts": []}}
    timing = {"actual_duration_ms": 2000, "started_at": "2026-01-01T00:00:00Z", "finished_at": "2026-01-01T00:00:02Z"}
    phases = [{"name": "baseline", **timing}]
    kinds = ("tcp_latency", "tcp_bandwidth", "tcp_timeout", "tcp_reset") if case == "tcp-seq" else ("tcp_timeout",)
    for index, kind in enumerate(kinds):
        phases.extend([{"name": "fault" + str(index), **timing, "chaos": {"rules_installed": 1, "matches": [{"matches": 8, "min_matches": 1, "rule": {"kind": kind}}]}, "connection_attempts": {"passed": True, "request": "dependency-probe", "max_attempts_per_request": 2, "attempts": 8}}, {"name": "recovery" + str(index), **timing, "step_recovery": {"dependency-probe": {"passed": True}}}])
    leaks = {"source": "application", "path": "/" if case == "conn-gauge" else "/connections", "samples": 80, "baseline": 0, "instance_id": str(uuid.uuid4()), "passed": True, "post_drain": {"passed": True, "remaining": 0, "limit": 0, "samples": 11}}
    if case == "conn-gauge": leaks.update(passed=False, baseline=None, error="connection baseline unavailable")
    load = {"chaos_phases": phases, "streams": [{"name": "dependency"}, {"name": "health"}], "thresholds": [{"passed": True}], "connection_leaks": leaks}
    if case in ("pool-full", "pool-queue", "conn-gauge", "tcp-cancel"):
        load["pool_exhaustion"] = {"source": "application", "passed": case != "pool-queue", "peak_active": 2, "peak_queued": 2, "max_wait_ms": 201, "budget": {"capacity": 2, "max_queue": 1 if case == "pool-queue" else 2}, "post_drain": {"passed": case != "pool-queue", "snapshot": {"active": 0, "queued": 0, "waiters_created": 12, "waiters_finished": 12}}}
        if case == "pool-queue": load["pool_exhaustion"]["error"] = "pool capacity, queue depth or wait budget exceeded"
        phases[1]["pool_exhaustion"] = {"passed": True, "queued_requests": 12}
        phases[2]["pool_exhaustion"] = {"passed": True, "windows": [{"passed": True, "required": 5, "completed": 20}]}
    if case in ("req-timeout", "req-body"):
        for index, phase in enumerate(phases):
            failed = case == "req-body" and index == 1
            phase["request_deadlines"] = {"dependency-probe": {"within_ms": 750, "min_samples": 3, "requests": 8, "max_ms": 520, "passed": not failed, "expected_status": 503 if failed else 504 if index == 1 else 200, "failures": 8 if failed else 0}}
    for phase in phases:
        phase["steps"] = [{"name": "health", "requests": 10, "thresholds": [{"passed": True}]}]
    steps[1]["load"] = load


def pool_cancel_proof(report, marker):
    base = {"namespace": report["run_id"], "instance_id": str(uuid.uuid4()), "capacity": 2, "active": 0, "queued": 0, "active_connections": 0, "queue_timers": 0, "deadline_timers": 0, "queue_deadline_ms": 10000, "attempt_deadline_ms": 30000, "waiters_created": 0, "waiters_finished": 0, "waiters_cancelled": 0, "waiters_timed_out": 0, "application_timeouts": 0, "cancelled_requests": 0, "queued_request_cancellations": 0}
    active = dict(base, active=2, queued=2, active_connections=2, queue_timers=2, deadline_timers=2, waiters_created=2)
    clean = dict(base, waiters_created=2, waiters_finished=2, waiters_cancelled=2, cancelled_requests=4, queued_request_cancellations=2)
    return {"namespace": report["run_id"], "marker": marker, "app_url": "http://127.0.0.1:12345", "baseline": base, "observed": active, "checkpoint": dict(active), "cleanup": clean, "cleanup_elapsed_ms": 20}


def auth_proof(report):
    namespace = report["run_id"]
    identity = str(uuid.uuid4())
    proof = {"namespace": namespace, "instance_id": identity, "backend_baseline_pids": [10],
             "baseline": {"namespace": namespace, "instance_id": identity, "attempts": 1, "tls_connections_verified": 1, "authentication_challenges_seen": 1}}
    for stage, attempts in (("outage", 4), ("recovery", 6), ("cleanup", 6)):
        status = {key: 0 for key in ("active_queries", "pool_total", "pool_waiting", "connections", "pending_connections", "pending_authentication", "active_connection_stalls", "connection_stall_timers")}
        status.update(namespace=namespace, instance_id=identity, attempts=attempts, tls_connections_verified=attempts, connection_stalls_fired=3,
                      connection_stall_enabled=stage == "outage", stopped=stage == "cleanup",
                      connection_stalls=[{"phase": "authentication", "outcome": "canceled", "configured_ms": 5000, "elapsed_ms": 2000} for _ in range(3)])
        proof[stage] = {"status": status, "database": {"namespace": namespace, "passed": True, "backend_sessions": 0, "unretired_backends": 0}}
    proof["outage"]["responses"] = [503, 503, 503]
    proof["recovery"].update(responses=[200, 200], value=42)
    return proof


def commit_proof(report, mode="drop"):
    deadline, request_held = mode != "drop", mode == "request-deadline"
    namespace, identity, ledger = report["run_id"], str(uuid.uuid4()), str(uuid.uuid4())
    proof = {"namespace": namespace, "fault_mode": mode, "instance_id": identity, "ledger_id": ledger, "backend_baseline_pids": [10]}
    for stage, attempts, commits in (("baseline", 0, 0), ("unknown", 1, 0 if request_held else 1), ("replay", 3, 1 if request_held else 2), ("cleanup", 3, 1 if request_held else 2)):
        status = {key: 0 for key in ("active_writes", "queued_writes", "pool_total", "pool_waiting", "connections", "pending_connections", "pending_authentication", "active_faults", "fault_timers", "tls_failures", "invalid_frames")}
        status.update(namespace=namespace, fault_mode=mode, instance_id=identity, ledger_id=ledger, attempts=attempts, commits_forwarded=commits,
                      acknowledgements_dropped=0 if stage == "baseline" or deadline else 1, tls_connections_verified=1, armed=False, stopped=stage == "cleanup",
                      faults_fired=0 if stage == "baseline" else 1, faults=[] if stage == "baseline" else [{"mode": "stall" if deadline else "drop", "direction": "request" if request_held else "response", "configured_ms": 5000 if deadline else 0, "outcome": "canceled" if deadline else "dropped", "elapsed_ms": 4000}])
        proof[stage] = status if stage == "baseline" else {"status": status, "retirement": {"namespace": namespace, "passed": True, "backend_sessions": 0, "unretired_backends": 0}}
    proof["unknown"].update(response={"status": 503, "body": {"attempts": 1, "statuses": [503], "body_errors": [], "error": "payment_unavailable", "payment_outcome": "unknown", "retryable": True}}, database={"ledger_id": ledger, "charges": 1, "pending": 0, "matching": 1})
    proof["replay"].update(response={"status": 200, "body": {"attempts": 1, "statuses": [201], "body_errors": [], "id": "ack-loss", "status": "confirmed"}}, conflict={"status": 409, "body": {"attempts": 1, "statuses": [409], "body_errors": [], "error": "idempotency_conflict"}}, database={"ledger_id": ledger, "charges": 1, "pending": 0, "matching": 1})
    proof["cleanup"]["database"] = {"namespace": namespace, "passed": True, "remaining": 0}
    if deadline:
        proof["unknown"].update(deadline_ms=1500, elapsed_ms=1500)
        proof["unknown"]["response"]["body"] = {"error": "payment_unavailable", "payment_outcome": "unknown", "retryable": True}
    if request_held:
        proof["unknown"]["database"].update(charges=0, matching=0)
    return proof


def bandwidth_proof(report):
    namespace, identity = report["run_id"], str(uuid.uuid4())
    proof = {"namespace": namespace, "instance_id": identity, "backend_baseline_pids": [10]}
    complete = {"status": 200, "body": {"ready": True, "bytes": 65536, "sha256": hashlib.sha256(b"x" * 65536).hexdigest()}}
    for stage, attempts, active, count in (("baseline", 1, 0, 0), ("limited", 2, 1, 1), ("timeout", 3, 1, 2), ("recovery", 4, 0, 2), ("cleanup", 4, 0, 2)):
        status = {key: 0 for key in ("active_queries", "pool_total", "pool_waiting", "connections", "pending_connections", "pending_authentication", "bandwidth_pending_transfers", "bandwidth_buffered_bytes", "bandwidth_timers", "tls_failures", "invalid_frames")}
        status.update(namespace=namespace, fixture_mode="bandwidth", instance_id=identity, attempts=attempts, tls_connections_verified=attempts, bandwidth_active_limits=active, stopped=stage == "cleanup")
        status["bandwidth"] = [{"direction": "response", "bytes_per_second": 262144 if index == 0 else 16384, "bytes_forwarded": 66000 if index == 0 else 24000, "canceled_transfers": 0 if index == 0 else 1, "elapsed_ms": 300 if index == 0 else 1600, "outcome": "active" if stage == "limited" or stage == "timeout" and index == 1 else "cleared"} for index in range(count)]
        proof[stage] = status if stage == "baseline" else {"status": status, "database": {"namespace": namespace, "passed": True, "backend_sessions": 0, "unretired_backends": 0}}
        if stage in ("limited", "recovery"):
            proof[stage]["response"] = complete
            proof[stage]["elapsed_ms"] = 400
    proof["timeout"].update(elapsed_ms=1600, response={"status": 503, "body": {"error": "dependency_unavailable", "retryable": True}})
    return proof


def read_cut_proof(report):
    namespace, identity = report["run_id"], str(uuid.uuid4())
    proof = {"namespace": namespace, "instance_id": identity, "backend_baseline_pids": [10]}
    complete = {"status": 200, "body": {"ready": True, "bytes": 65536, "sha256": hashlib.sha256(b"x" * 65536).hexdigest()}}
    failed = {"status": 503, "body": {"error": "dependency_unavailable", "retryable": True}}
    for stage, attempts, count, pairs in (("baseline", 1, 0, 0), ("interrupted", 2, 1, 0), ("recovery", 3, 1, 0), ("concurrent", 5, 2, 1), ("final", 6, 2, 1), ("cleanup", 6, 2, 1)):
        status = {key: 0 for key in ("active_queries", "pool_total", "pool_waiting", "connections", "pending_connections", "pending_authentication", "bandwidth_active_limits", "bandwidth_pending_transfers", "bandwidth_buffered_bytes", "bandwidth_timers", "active_read_cuts", "read_pair_waiters", "read_pair_timers", "tls_failures", "invalid_frames")}
        status.update(namespace=namespace, fixture_mode="read-cut", instance_id=identity, attempts=attempts, tls_connections_verified=attempts, read_cut_armed=False, read_pair_armed=False, stopped=stage == "cleanup", read_cuts_fired=count, concurrent_read_pairs=pairs,
                      read_cuts=[{"outcome": "cut", "after_bytes": 2048, "prefix_flushed_bytes": 2048, "frame_bytes": 65547} for _ in range(count)])
        proof[stage] = status if stage == "baseline" else {"status": status, "database": {"namespace": namespace, "passed": True, "backend_sessions": 0, "unretired_backends": 0}}
        if stage not in ("baseline", "cleanup"):
            proof[stage].update(responses=[failed] if stage == "interrupted" else [complete, failed] if stage == "concurrent" else [complete], elapsed_ms=100)
    return proof


def ssl_request_proof(report):
    namespace, identity = report["run_id"], str(uuid.uuid4())
    proof = {"namespace": namespace, "instance_id": identity, "backend_baseline_pids": [10]}
    failed = {"status": 503, "body": {"error": "dependency_unavailable", "retryable": True}}
    complete = {"status": 200, "body": {"ready": True, "value": 42}}
    for stage, attempts, verified, count in (("baseline", 1, 1, 0), ("retries", 4, 1, 3), ("queued", 7, 1, 5), ("recovery", 9, 3, 5), ("cleanup", 9, 3, 5)):
        status = {key: 0 for key in ("active_queries", "pool_total", "pool_waiting", "connections", "pending_connections", "pending_authentication", "active_connection_stalls", "connection_stall_timers", "invalid_frames")}
        status.update(namespace=namespace, fixture_mode="ssl-request", instance_id=identity, attempts=attempts, tls_connections_verified=verified, authentication_challenges_seen=verified, connection_stalls_fired=count, connection_attempts=count+verified, ssl_requests_seen=count+verified, tls_failures=count, peak_pool_waiting=0 if stage in ("baseline", "retries") else 2, stopped=stage == "cleanup", connection_stall_enabled=stage in ("retries", "queued"),
                      connection_stalls=[{"phase": "ssl_request", "leg": "client", "configured_ms": 5000, "elapsed_ms": 2000, "outcome": "canceled"} for _ in range(count)])
        proof[stage] = status if stage == "baseline" else {"status": status, "database": {"namespace": namespace, "passed": True, "backend_sessions": 0, "unretired_backends": 0}}
        if stage in ("retries", "queued"):
            proof[stage].update(responses=[failed]*3, elapsed_ms=6000 if stage == "retries" else 2000)
        elif stage == "recovery":
            proof[stage]["responses"] = [complete]*2
    return proof


def retry_budget_proof(report):
    namespace, caller_id, provider_id = report["run_id"], str(uuid.uuid4()), str(uuid.uuid4())
    proof = {"namespace": namespace, "caller_id": caller_id, "provider_id": provider_id, "backend_baseline_pids": [10]}
    for stage, originals, attempts, verified, count in (("baseline", 1, 1, 1, 0), ("cancellation", 2, 2, 1, 1), ("outage", 8, 10, 1, 3), ("recovery", 10, 12, 3, 3), ("cleanup", 10, 12, 3, 3)):
        failed = stage in ("outage", "recovery", "cleanup")
        caller = {"namespace": namespace, "instance_id": caller_id, "stopped": stage == "cleanup", "originals": originals, "dependency_attempts": attempts, "retries_admitted": 2 if failed else 0, "retries_denied": 4 if failed else 0, "remaining": 0 if failed else 2, "window_id": 1 if failed else 0, "active_requests": 0, "retry_timers": 0, "deadline_timers": 0, "budget_capacity": 2, "budget_window_ms": 30000, "overall_deadline_ms": 4500, "attempt_deadline_ms": 1500, "max_attempts": 2}
        provider = {key: 0 for key in ("active_queries", "pool_total", "pool_waiting", "connections", "pending_connections", "pending_authentication", "active_connection_stalls", "connection_stall_timers", "invalid_frames")}
        provider.update(namespace=namespace, fixture_mode="ssl-request", instance_id=provider_id, stopped=stage == "cleanup", attempts=attempts, tls_connections_verified=verified, authentication_challenges_seen=verified, connection_stalls_fired=count, connection_attempts=count+verified, ssl_requests_seen=count+verified, tls_failures=count, peak_pool_waiting=7 if failed else 0, connection_stall_enabled=stage in ("cancellation", "outage"), connection_stalls=[{"phase": "ssl_request", "leg": "client", "outcome": "canceled", "configured_ms": 5000, "elapsed_ms": 2000} for _ in range(count)])
        state = {"caller": caller, "provider": provider}
        proof[stage] = state if stage == "baseline" else {"status": state, "database": {"namespace": namespace, "passed": True, "backend_sessions": 0, "unretired_backends": 0}}
    proof["cancellation"].update(joined=True, client_aborted=True)
    proof["outage"].update(elapsed_ms=3100, responses=[{"status": 503, "body": {"error": "dependency_unavailable", "retryable": True, "attempts": count}} for count in [1,1,1,1,2,2]])
    proof["recovery"]["responses"] = [{"status": 200, "body": {"ready": True, "value": 42, "attempts": 1}}]*2
    return proof


def circuit_breaker_proof(report):
    namespace, caller_id, provider_id = report["run_id"], str(uuid.uuid4()), str(uuid.uuid4())
    proof = {"namespace": namespace, "caller_id": caller_id, "provider_id": provider_id, "backend_baseline_pids": [10]}
    failed = {"status": 503, "body": {"error": "dependency_unavailable", "retryable": True, "attempts": 1}}
    blocked = {"status": 503, "body": {"error": "circuit_open", "retryable": True, "attempts": 0}}
    for stage, requests, attempts, count, state, opens, halves, closes, denied, probes in (
            ("baseline", 1, 1, 0, "closed", 0, 0, 0, 0, 0), ("trip", 3, 3, 2, "open", 1, 0, 0, 0, 0), ("open", 9, 3, 2, "open", 1, 0, 0, 6, 0), ("failed_probe", 14, 4, 3, "open", 2, 1, 0, 10, 1), ("cancellation", 15, 5, 4, "open", 3, 2, 0, 10, 2), ("recovery", 18, 8, 4, "closed", 3, 3, 1, 10, 3), ("cleanup", 18, 8, 4, "closed", 3, 3, 1, 10, 3)):
        caller = {"namespace": namespace, "instance_id": caller_id, "state": state, "stopped": stage == "cleanup", "failure_threshold": 2, "cooldown_ms": 3000, "deadline_ms": 1500, "requests": requests, "dependency_attempts": attempts, "open_transitions": opens, "half_open_transitions": halves, "closed_transitions": closes, "generation": opens+halves+closes, "blocked": denied, "probes_started": probes, "peak_probes": 1 if probes else 0, "probe_failures": 1 if probes else 0, "probe_cancellations": 1 if probes >= 2 else 0, "probe_successes": 1 if closes else 0, "consecutive_failures": 2 if state == "open" else 0, "active_requests": 0, "active_probes": 0, "deadline_timers": 0}
        verified = 4 if stage in ("recovery", "cleanup") else 1
        provider = {key: 0 for key in ("active_queries", "pool_total", "pool_waiting", "connections", "pending_connections", "pending_authentication", "active_connection_stalls", "connection_stall_timers", "invalid_frames", "peak_pool_waiting")}
        provider.update(namespace=namespace, instance_id=provider_id, fixture_mode="ssl-request", stopped=stage == "cleanup", attempts=attempts, tls_connections_verified=verified, authentication_challenges_seen=verified, connection_stalls_fired=count, connection_attempts=count+verified, ssl_requests_seen=count+verified, tls_failures=count, connection_stall_enabled=stage not in ("baseline", "recovery", "cleanup"), connection_stalls=[{"phase": "ssl_request", "leg": "client", "outcome": "canceled", "configured_ms": 5000, "elapsed_ms": 2000} for _ in range(count)])
        status = {"caller": caller, "provider": provider}
        proof[stage] = status if stage == "baseline" else {"status": status, "database": {"namespace": namespace, "passed": True, "backend_sessions": 0, "unretired_backends": 0}}
    proof["trip"]["responses"] = [failed]*2
    proof["open"].update(responses=[blocked]*6, elapsed_ms=100)
    proof["failed_probe"].update(probe=failed, responses=[blocked]*4)
    proof["cancellation"].update(joined=True, client_aborted=True)
    for stage, attempts, probes in (("failed_probe", 4, 1), ("cancellation", 5, 2)):
        proof[stage]["observed"] = {"namespace": namespace, "instance_id": caller_id, "state": "half-open", "active_probes": 1, "active_requests": 1, "dependency_attempts": attempts, "probes_started": probes}
    proof["recovery"]["responses"] = [{"status": 200, "body": {"ready": True, "value": 42, "attempts": 1}}]*3
    return proof


def bulkhead_proof(report):
    namespace, caller_id, provider_id = report["run_id"], str(uuid.uuid4()), str(uuid.uuid4())
    proof = {"namespace": namespace, "caller_id": caller_id, "provider_id": provider_id, "backend_baseline_pids": [10]}
    for stage, requests, attempts, peak_queue, admissions, timeouts, rejects, count in (("baseline", 1, 1, 0, 0, 0, 0, 0), ("cancellation", 4, 3, 1, 1, 0, 0, 2), ("outage", 10, 5, 2, 3, 2, 2, 4), ("recovery", 12, 7, 2, 3, 2, 2, 4), ("cleanup", 12, 7, 2, 3, 2, 2, 4)):
        caller = {"namespace": namespace, "instance_id": caller_id, "stopped": stage == "cleanup", "capacity": 2, "queue_capacity": 2, "queue_deadline_ms": 1000, "attempt_deadline_ms": 2500, "overall_deadline_ms": 3500, "requests": requests, "dependency_attempts": attempts, "permits_granted": attempts, "permits_released": attempts, "peak_active": 1 if stage == "baseline" else 2, "peak_queue": peak_queue, "queued_admissions": admissions, "queue_timeouts": timeouts, "rejected": rejects, "queue_cancellations": 0 if stage == "baseline" else 1, "health_requests": 0 if stage in ("baseline", "cancellation") else 3, "active_requests": 0, "active_dependencies": 0, "queued_requests": 0, "queue_timers": 0, "deadline_timers": 0}
        verified = 3 if stage in ("recovery", "cleanup") else 1
        provider = {key: 0 for key in ("active_queries", "pool_total", "pool_waiting", "connections", "pending_connections", "pending_authentication", "active_connection_stalls", "connection_stall_timers", "invalid_frames")}
        provider.update(namespace=namespace, instance_id=provider_id, fixture_mode="ssl-request", stopped=stage == "cleanup", attempts=attempts, tls_connections_verified=verified, authentication_challenges_seen=verified, connection_stalls_fired=count, connection_attempts=count+verified, ssl_requests_seen=count+verified, tls_failures=count, peak_pool_waiting=0 if stage == "baseline" else 1, connection_stall_enabled=stage in ("cancellation", "outage"), connection_stalls=[{"phase": "ssl_request", "leg": "client", "outcome": "canceled", "configured_ms": 5000, "elapsed_ms": 2000} for _ in range(count)])
        state = {"caller": caller, "provider": provider}
        proof[stage] = state if stage == "baseline" else {"status": state, "database": {"namespace": namespace, "passed": True, "backend_sessions": 0, "unretired_backends": 0}}
    proof["cancellation"].update(client_aborted=True, joined=True)
    for stage, queued in (("cancellation", 1), ("outage", 2)):
        proof[stage]["observed"] = {"namespace": namespace, "instance_id": caller_id, "active_dependencies": 2, "queued_requests": queued}
    proof["outage"]["responses"] = [{"status": 503, "body": {"error": error, "retryable": True, "attempts": 1 if error == "dependency_unavailable" else 0}, "elapsed_ms": 2000 if error == "dependency_unavailable" else 1000 if error == "queue_timeout" else 100} for error in ["bulkhead_rejected"]*2 + ["dependency_unavailable"]*2 + ["queue_timeout"]*2]
    proof["outage"]["health"] = [{"status": 200, "body": {"ready": True, "active_dependencies": 2}, "elapsed_ms": 100}]*3
    proof["recovery"]["responses"] = [{"status": 200, "body": {"ready": True, "value": 42, "attempts": 1}}]*2
    return proof


def combined_proof(report):
    namespace, caller_id, provider_id = report["run_id"], str(uuid.uuid4()), str(uuid.uuid4())
    proof = {"namespace": namespace, "caller_id": caller_id, "provider_id": provider_id, "backend_baseline_pids": [10]}
    for stage, requests, attempts, state, opens, halves, closes, probes, denied, canceled, count in (
            ("baseline", 1, 1, "closed", 0, 0, 0, 0, 0, 0, 0), ("initial_cancellation", 4, 3, "closed", 0, 0, 0, 0, 0, 3, 2), ("outage", 10, 7, "open", 1, 0, 0, 0, 0, 3, 4), ("open", 16, 7, "open", 1, 0, 0, 0, 6, 3, 4), ("failed_probe", 21, 8, "open", 2, 1, 0, 1, 10, 3, 5), ("cancellation", 22, 9, "open", 3, 2, 0, 2, 10, 4, 6), ("recovery", 25, 12, "closed", 3, 3, 1, 3, 10, 4, 6), ("cleanup", 25, 12, "closed", 3, 3, 1, 3, 10, 4, 6)):
        stressed = stage not in ("baseline", "initial_cancellation")
        caller = {"namespace": namespace, "instance_id": caller_id, "stopped": stage == "cleanup", "capacity": 2, "queue_capacity": 2, "queue_deadline_ms": 1000, "attempt_deadline_ms": 1500, "overall_deadline_ms": 4500, "failure_threshold": 4, "cooldown_ms": 3000, "budget_capacity": 2, "budget_window_ms": 30000,
                  "requests": requests, "dependency_attempts": attempts, "permit_protected_attempts": attempts, "permits_granted": attempts, "permits_released": attempts,
                  "peak_active": 1 if stage == "baseline" else 2, "peak_queue": 2 if stressed else 1 if stage == "initial_cancellation" else 0, "queued_admissions": 3 if stressed else 1 if stage == "initial_cancellation" else 0, "queue_timeouts": 2 if stressed else 0, "rejected": 2 if stressed else 0, "queue_cancellations": 0 if stage == "baseline" else 1, "health_requests": 3 if stressed else 0,
                  "state": state, "generation": opens+halves+closes, "consecutive_failures": 4 if state == "open" else 0, "open_transitions": opens, "half_open_transitions": halves, "closed_transitions": closes, "blocked": denied, "blocked_after_acquire": 0,
                  "probes_started": probes, "peak_probes": 1 if probes else 0, "probe_failures": 1 if probes else 0, "probe_cancellations": 1 if probes >= 2 else 0, "probe_successes": 1 if closes else 0, "request_cancellations": canceled,
                  "retries_admitted": 2 if stressed else 0, "retries_denied": 0, "remaining": 0 if stressed else 2, "window_id": 1 if stressed else 0,
                  "active_requests": 0, "active_dependencies": 0, "active_probes": 0, "queued_requests": 0, "queue_timers": 0, "retry_timers": 0, "deadline_timers": 0}
        verified = 4 if stage in ("recovery", "cleanup") else 1
        provider = {key: 0 for key in ("active_queries", "pool_total", "pool_waiting", "connections", "pending_connections", "pending_authentication", "active_connection_stalls", "connection_stall_timers", "invalid_frames")}
        provider.update(namespace=namespace, instance_id=provider_id, fixture_mode="ssl-request", stopped=stage == "cleanup", attempts=attempts, tls_connections_verified=verified, authentication_challenges_seen=verified, connection_stalls_fired=count, connection_attempts=count+verified, ssl_requests_seen=count+verified, tls_failures=count, peak_pool_waiting=0 if stage == "baseline" else 1 if stage == "initial_cancellation" else 3, connection_stall_enabled=stage not in ("baseline", "recovery", "cleanup"), connection_stalls=[{"phase": "ssl_request", "leg": "client", "outcome": "canceled", "configured_ms": 5000, "elapsed_ms": 2000} for _ in range(count)])
        status = {"caller": caller, "provider": provider}
        proof[stage] = status if stage == "baseline" else {"status": status, "database": {"namespace": namespace, "passed": True, "backend_sessions": 0, "unretired_backends": 0}}
    for stage in ("initial_cancellation", "cancellation"):
        proof[stage].update(joined=True, client_aborted=True)
    for stage, queued in (("initial_cancellation", 1), ("outage", 2)):
        proof[stage]["observed"] = {"namespace": namespace, "instance_id": caller_id, "active_dependencies": 2, "queued_requests": queued}
    for stage, attempts in (("failed_probe", 8), ("cancellation", 9)):
        proof[stage]["observed"] = {"namespace": namespace, "instance_id": caller_id, "state": "half-open", "active_probes": 1, "active_dependencies": 1, "dependency_attempts": attempts}
    proof["outage"]["responses"] = [{"status": 503, "body": {"error": error, "retryable": True, "attempts": 2 if error == "dependency_unavailable" else 0}, "elapsed_ms": 3100 if error == "dependency_unavailable" else 1000 if error == "queue_timeout" else 100} for error in ["bulkhead_rejected"]*2+["dependency_unavailable"]*2+["queue_timeout"]*2]
    proof["outage"]["health"] = [{"status": 200, "body": {"ready": True, "active_dependencies": 2}, "elapsed_ms": 100}]*3
    blocked = {"status": 503, "body": {"error": "circuit_open", "retryable": True, "attempts": 0}}
    proof["open"].update(responses=[blocked]*6, elapsed_ms=100)
    proof["failed_probe"].update(probe={"status": 503, "body": {"error": "dependency_unavailable", "retryable": True, "attempts": 1}}, responses=[blocked]*4)
    proof["recovery"]["responses"] = [{"status": 200, "body": {"ready": True, "value": 42, "attempts": 1}}]*3
    return proof


def repeated_proof(report):
    base = combined_proof(report)
    proof = {key: base[key] for key in ("namespace", "caller_id", "provider_id", "backend_baseline_pids")}
    def snapshot(requests, attempts, grants, cycle, recovered, denied, verified, count, baseline=False, early=False, stopped=False):
        closed = recovered or baseline or early
        probes = cycle if recovered or early else max(0, cycle-1)
        caller = dict(base["baseline"]["caller"])
        caller.update({'capacity': 2, 'queue_capacity': 2, 'queue_deadline_ms': 1000, 'attempt_deadline_ms': 1500, 'overall_deadline_ms': 4500, 'failure_threshold': 4, 'cooldown_ms': 3000, 'budget_capacity': 2, 'budget_window_ms': 30000, 'requests': requests, 'dependency_attempts': attempts, 'permit_protected_attempts': attempts, 'permits_granted': grants, 'permits_released': grants, 'peak_active': 1 if baseline else 2, 'peak_queue': 0 if baseline else 2, 'queued_admissions': cycle * 2, 'queue_timeouts': cycle * 2, 'rejected': cycle * 2, 'queue_cancellations': 0, 'health_requests': cycle * 3, 'state': 'closed' if closed else 'open', 'generation': cycle + 2 * probes, 'consecutive_failures': 1 if early and (not recovered) else 0 if closed else 4, 'open_transitions': cycle, 'half_open_transitions': probes, 'closed_transitions': probes, 'probes_started': probes, 'probe_successes': probes, 'peak_probes': 1 if probes else 0, 'probe_failures': 0, 'probe_cancellations': 0, 'blocked': denied, 'blocked_after_acquire': 0, 'request_cancellations': 0, 'retries_admitted': cycle * 2, 'retries_denied': 0 if baseline or (cycle == 1 and (not early)) else 1, 'remaining': 2 if baseline else 0, 'window_id': cycle})
        caller.update(stopped=stopped, retry_window_age_ms=6000, retry_window_remaining_ms=24000)
        provider = dict(base["baseline"]["provider"])
        provider.update(stopped=stopped, attempts=attempts, tls_connections_verified=verified, authentication_challenges_seen=verified, connection_stalls_fired=count, connection_attempts=count+verified, ssl_requests_seen=count+verified, tls_failures=count, peak_pool_waiting=0 if baseline else 3, connection_stall_enabled=not closed or early and not recovered, connection_stalls=[{"phase":"ssl_request","leg":"client","outcome":"canceled","configured_ms":5000,"elapsed_ms":2000} for _ in range(count)])
        status = {"caller":caller,"provider":provider}
        return status if baseline else {"status":status,"database":{"namespace":proof["namespace"],"passed":True,"backend_sessions":0,"unretired_backends":0}}
    proof["baseline"] = snapshot(1,1,1,0,True,0,1,0,baseline=True)
    complete = {"status":200,"body":{"ready":True,"value":42,"attempts":1}}
    proof["before_expiry"] = snapshot(17,9,10,1,False,6,4,3,early=True)
    proof["before_expiry"]["response"] = {"status":503,"body":{"error":"retry_budget_exhausted","retryable":True,"attempts":1}}
    proof["before_expiry"]["recovery"] = snapshot(18,10,11,1,True,6,5,3,early=True)
    proof["before_expiry"]["recovery"]["response"] = complete
    proof["cycles"] = []
    for cycle, count in ((1,2),(2,5),(3,7)):
        extra = 2 if cycle>1 else 0
        requests, attempts = 15*cycle-8+extra, 7*cycle-2+extra
        grants, verified = attempts+(1 if cycle>1 else 0), 1+3*(cycle-1)+(1 if cycle>1 else 0)
        block = {"index":cycle,"outage":snapshot(requests,attempts,grants,cycle,False,6*(cycle-1),verified,count),"open":snapshot(requests+6,attempts,grants,cycle,False,6*cycle,verified,count),"recovery":snapshot(requests+9,attempts+3,grants+3,cycle,True,6*cycle,verified+3,count)}
        for field in ("responses","health","observed"):
            block["outage"][field] = base["outage"][field]
        block["open"].update(responses=base["open"]["responses"],elapsed_ms=100)
        block["recovery"]["responses"] = [complete]*3
        if cycle>1:
            before = dict(proof["before_expiry"]["status"]["caller"],window_id=cycle-1,retry_window_age_ms=10000,retry_window_remaining_ms=20000)
            after = dict(before,retry_window_age_ms=30000,retry_window_remaining_ms=0)
            block["expiry"] = {"before":before,"after":after,"elapsed_ms":20000}
        proof["cycles"].append(block)
    proof["cleanup"] = snapshot(48,24,25,3,True,18,11,7,stopped=True)
    return proof


def isolation_proof(report):
    base = combined_proof(report)
    namespace = report["run_id"]
    proof = {"namespace": namespace, "caller_id": str(uuid.uuid4()), "provider_ids": {name: str(uuid.uuid4()) for name in ("a", "b")}, "dependency_ids": {name: str(uuid.uuid4()) for name in ("a", "b")}, "backend_baseline_pids": [10]}
    def snapshot(expected, health, stopped=False, baseline=False):
        dependencies, providers = {}, {}
        for name, (requests, attempts, state) in expected.items():
            faulted, recovered = state != "untouched", state == "recovered"
            value = dict(base["baseline"]["caller"])
            value.update(instance_id=proof["dependency_ids"][name], stopped=stopped, requests=requests, dependency_attempts=attempts, permit_protected_attempts=attempts, permits_granted=attempts, permits_released=attempts,
                         peak_active=2 if faulted else 1, peak_queue=2 if faulted else 0, queued_admissions=2 if faulted else 0, queue_timeouts=2 if faulted else 0, rejected=2 if faulted else 0, queue_cancellations=0, request_cancellations=0, health_requests=0,
                         blocked=6 if state in ("open", "recovered") else 0, retries_admitted=2 if faulted else 0, remaining=0 if faulted else 2, window_id=1 if faulted else 0,
                         state="closed" if recovered or not faulted else "open", generation=3 if recovered else 1 if faulted else 0, consecutive_failures=0 if recovered or not faulted else 4,
                         open_transitions=1 if faulted else 0, half_open_transitions=1 if recovered else 0, closed_transitions=1 if recovered else 0, probes_started=1 if recovered else 0, probe_successes=1 if recovered else 0, peak_probes=1 if recovered else 0)
            dependencies[name] = value
            count, verified = (2 if faulted else 0), attempts - (4 if faulted else 0)
            provider = dict(base["baseline"]["provider"])
            provider.update(instance_id=proof["provider_ids"][name], stopped=stopped, attempts=attempts, tls_connections_verified=verified, authentication_challenges_seen=verified, connection_stalls_fired=count, connection_attempts=count+verified, ssl_requests_seen=count+verified, tls_failures=count, peak_pool_waiting=3 if faulted else 0, connection_stall_enabled=faulted and not recovered, connection_stalls=[{"phase":"ssl_request","leg":"client","outcome":"canceled","configured_ms":5000,"elapsed_ms":2000} for _ in range(count)])
            providers[name] = provider
        status = {"caller": {"namespace": namespace, "instance_id": proof["caller_id"], "stopped": stopped, "health_requests": health, "dependencies": dependencies}, "providers": providers}
        return status if baseline else {"status": status, "database": {"namespace": namespace, "passed": True, "backend_sessions": 0, "unretired_backends": 0}}
    proof["baseline"] = snapshot({"a": (1,1,"untouched"), "b": (1,1,"untouched")},0,baseline=True)
    counters = [
        ({"a": (7,5,"outage"), "b": (7,7,"untouched")}, {"a": (13,5,"open"), "b": (10,10,"untouched")}, {"a": (16,8,"recovered"), "b": (10,10,"untouched")}),
        ({"a": (22,14,"recovered"), "b": (16,14,"outage")}, {"a": (25,17,"recovered"), "b": (22,14,"open")}, {"a": (25,17,"recovered"), "b": (25,17,"recovered")})]
    complete = {"status":200,"body":{"ready":True,"value":42,"attempts":1}}
    proof["phases"] = []
    for index, faulted in enumerate(("a","b")):
        healthy = "b" if faulted == "a" else "a"
        block = {"faulted":faulted,"healthy":healthy}
        for stage, expected in zip(("outage","open","recovery"),counters[index]):
            block[stage] = snapshot(expected,(index+1)*3)
        block["outage"].update(responses=base["outage"]["responses"],healthy_responses=[{**complete,"elapsed_ms":100}]*6,health=[{"status":200,"body":{"ready":True,"active_dependencies":{faulted:2,healthy:0}},"elapsed_ms":100}]*3)
        block["open"].update(responses=base["open"]["responses"],elapsed_ms=100,healthy_responses=[{**complete,"elapsed_ms":100}]*3)
        block["recovery"]["responses"] = [complete]*3
        observed = copy.deepcopy(block["outage"]["status"]["caller"])
        observed["dependencies"][faulted].update(active_dependencies=2,queued_requests=2)
        block["observed"] = observed; block["after_healthy"] = copy.deepcopy(observed)
        proof["phases"].append(block)
    proof["cleanup"] = snapshot(counters[-1][-1],6,stopped=True)
    return proof


class HarnessTests(unittest.TestCase):
    def test_all_cases_need_actual_expected_results(self):
        self.assertEqual(len(native.CASES), 38)
        for case in native.CASES:
            _, _, mode, _ = native.case_contract(case)
            with self.subTest(case=case):
                native.verify_report(receipt(case), case, 0 if mode == "success" else 1, mode == "cancel", "native-checkout-" + case)

    def test_live_checkpoint_rechecks_resources_before_interrupt(self):
        report = receipt("tcp-flight")[0]
        marker = "b" * 32
        for queued in (2, 0):
            with self.subTest(queued=queued), tempfile.TemporaryDirectory() as directory:
                path = Path(directory) / "proof.json"
                proof = pool_cancel_proof(report, marker); proof.pop("checkpoint")
                path.write_text(json.dumps(proof))
                snapshot = dict(proof["observed"], queued=queued)
                class Response:
                    status = 200
                    def __enter__(self): return self
                    def __exit__(self, *args): return False
                    def read(self, maximum): return json.dumps(snapshot).encode()
                class Opener:
                    def open(self, url, timeout): return Response()
                with patch.object(native.urllib.request, "build_opener", return_value=Opener()):
                    if queued == 2:
                        native.confirm_pool_checkpoint(path, marker)
                        self.assertEqual(json.loads(path.read_text())["checkpoint"], snapshot)
                    else:
                        with self.assertRaises(ValueError): native.confirm_pool_checkpoint(path, marker)
                        self.assertNotIn("checkpoint", json.loads(path.read_text()))

    def test_execute_rechecks_checkpoint_before_sending_sigint(self):
        marker = "d" * 32; checked = []
        code = "import time; print('" + marker + "', flush=True); time.sleep(10)"
        with tempfile.TemporaryDirectory() as directory:
            status, issued = native.execute([sys.executable, "-c", code], Path(directory)/"run.log", marker, deadline_seconds=3, cleanup_seconds=3, checkpoint=lambda: checked.append(True))
        self.assertTrue(issued); self.assertNotEqual(status, 0); self.assertEqual(checked, [True])

    def test_active_pool_proof_rejects_incomplete_or_natural_cleanup(self):
        report = receipt("tcp-flight")[0]
        good = pool_cancel_proof(report, "b" * 32)
        self.assertTrue(native.verify_pool_cancel_proof(good, report, "b" * 32)["passed"])
        granted_then_cancelled = copy.deepcopy(good); granted_then_cancelled["cleanup"]["waiters_cancelled"] = 0
        self.assertTrue(native.verify_pool_cancel_proof(granted_then_cancelled, report, "b" * 32)["passed"])
        mutations = [("checkpoint", "queued", 0), ("observed", "queue_timers", 0), ("cleanup", "active_connections", 1), ("cleanup", "deadline_timers", 1), ("cleanup", "waiters_finished", 1), ("cleanup", "queued_request_cancellations", 0), ("cleanup", "cancelled_requests", 0), ("cleanup", "waiters_timed_out", 2), ("cleanup", "waiters_timed_out", False), ("cleanup", "cancelled_requests", 5), ("cleanup", "instance_id", str(uuid.uuid4())), ("checkpoint", "namespace", "c" * 32)]
        for stage, field, value in mutations:
            proof = copy.deepcopy(good); proof[stage][field] = value
            with self.subTest(stage=stage, field=field), self.assertRaises(ValueError):
                native.verify_pool_cancel_proof(proof, report, "b" * 32)
        with self.assertRaises(ValueError): native.verify_pool_cancel_proof(good, report, "c" * 32)

    def test_transport_evidence_fails_closed(self):
        mutations = (
            ("pool-full", lambda load: load["pool_exhaustion"].update(max_wait_ms=301)),
            ("pool-full", lambda load: load["chaos_phases"][2]["pool_exhaustion"].update(windows=[])),
            ("tcp-seq", lambda load: load["chaos_phases"][1].update(steps=[])),
            ("req-timeout", lambda load: load["chaos_phases"][1]["request_deadlines"]["dependency-probe"].update(max_ms=float('nan'))),
            ("tcp-seq", lambda load: load["chaos_phases"][3]["chaos"]["matches"][0]["rule"].update(kind="tcp_timeout")),
            ("pool-full", lambda load: load["pool_exhaustion"]["post_drain"]["snapshot"].update(queued=1)),
            ("pool-full", lambda load: load["pool_exhaustion"].update(peak_active=1)),
            ("pool-queue", lambda load: load["pool_exhaustion"].update(peak_queued=1)),
            ("req-timeout", lambda load: load["chaos_phases"][1]["request_deadlines"]["dependency-probe"].update(max_ms=751)),
            ("req-timeout", lambda load: load["chaos_phases"][1]["request_deadlines"]["dependency-probe"].update(requests=0)),
            ("req-body", lambda load: load["chaos_phases"][1]["request_deadlines"]["dependency-probe"].update(failures=0)),
            ("conn-gauge", lambda load: load["connection_leaks"].update(error="unrelated error")),
            ("tcp-cancel", lambda load: load["connection_leaks"]["post_drain"].update(remaining=1)),
        )
        for case, mutate in mutations:
            document = receipt(case)
            mutate(document[0]["steps"][1]["load"])
            _, _, mode, _ = native.case_contract(case)
            with self.subTest(case=case), self.assertRaises(ValueError):
                native.verify_report(document, case, 0 if mode == "success" else 1, mode == "cancel")
        for case in native.TRANSPORT_CASES:
            document = receipt(case)
            document[0]["steps"][1]["load"].pop("connection_leaks")
            _, _, mode, _ = native.case_contract(case)
            with self.subTest(case=case), self.assertRaises(ValueError):
                native.verify_report(document, case, 0 if mode == "success" else 1, mode == "cancel")

    def test_strict_cases_reject_missing_or_wrong_evidence(self):
        for case in native.STRICT_CASES:
            for field, value in (("limit", 3), ("requests", 0), ("overflow", 1), ("capacity", 0), ("per_request", None)):
                document = receipt(case)
                budget = document[0]["steps"][1]["load"]["chaos_phases"][1]["retry_amplification"]
                if field == "per_request": budget.pop(field)
                else: budget["per_request"][field] = value
                with self.subTest(case=case, field=field), self.assertRaises(ValueError):
                    native.verify_report(document, case, 0 if case == "req-bounded" else 1, False)

    def test_in_flight_requires_pending_checkpoint_replacement_and_join(self):
        for field in ("pending_at_restart", "restart_completed", "joined"):
            document = receipt("flight-pending")
            document[0]["steps"][1]["restart"]["in_flight"][field] = False
            with self.subTest(field=field), self.assertRaises(ValueError):
                native.verify_report(document, "flight-pending", 0, False)
        for field in ("checkpoints", "storage_after", "readiness"):
            document = receipt("flight-pending")
            target = document[0]["steps"][1]["restart"]
            if field == "checkpoints":
                target = target["in_flight"]
            target[field] = []
            with self.subTest(field=field), self.assertRaises(ValueError):
                native.verify_report(document, "flight-pending", 0, False)
        document = receipt("flight-pending")
        document[0]["steps"][1]["restart"]["in_flight"]["outcome"] = "canceled_on_failure"
        with self.assertRaises(ValueError):
            native.verify_report(document, "flight-pending", 0, False)

    def test_restart_matrix_rejects_mismatched_or_missing_checkpoint(self):
        for case in native.RESTART_MATRIX:
            document = receipt(case)
            checkpoints = document[0]["steps"][1]["restart"]["in_flight"]["checkpoints"]
            checkpoints[1]["name"] = "wrong-checkpoint"
            with self.subTest(case=case), self.assertRaises(ValueError):
                native.verify_report(document, case, 0, False)
        document = receipt("flight-before")
        document[0]["scenario"] = "write-committed"
        with self.assertRaises(ValueError):
            native.verify_report(document, "flight-before", 0, False)

    def test_dependency_requires_service_replacement_and_exact_planned_wake(self):
        for case in native.DEPENDENCY_MATRIX:
            for field in ("workload", "planned", "unexpected"):
                document = receipt(case)
                if field == "workload":
                    document[0]["steps"][1]["restart"]["workload"] = "app"
                elif field == "planned":
                    document[0]["service_hot"]["payment"]["planned_restarts"] = []
                else:
                    document[0]["service_hot"]["payment"]["no_new_wake"] = False
                with self.subTest(case=case, field=field), self.assertRaises(ValueError):
                    native.verify_report(document, case, 0, False)

    def test_dependency_database_probe_requires_retired_exact_namespace(self):
        repo = Path(__file__).resolve().parents[2]
        report = receipt("dep-pending")[0]
        for proof in ({"namespace": report["run_id"], "passed": True, "remaining": 1},
                      {"namespace": "other", "passed": True, "remaining": 0}):
            with patch.object(native.subprocess, "run", return_value=native.subprocess.CompletedProcess([], 0, json.dumps(proof), "")), self.assertRaises(ValueError):
                native.verify_dependency_database(repo, report)
        proof = {"namespace": report["run_id"], "passed": True, "remaining": 0}
        with patch.object(native.subprocess, "run", return_value=native.subprocess.CompletedProcess([], 0, json.dumps(proof), "")):
            self.assertEqual(native.verify_dependency_database(repo, report), proof)

    def test_success_cannot_hide_missing_runtime_evidence(self):
        mutations = (
            lambda r: r.update(engine="simulated"),
            lambda r: r.update(status="skipped"),
            lambda r: r.update(skip_reason="no fleet"),
            lambda r: r.update(cleanup_error="namespace remains"),
            lambda r: r.update(phases=[]),
            lambda r: r.update(source_sha256=""),
            lambda r: r.update(workload_app_ids={}),
            lambda r: r["steps"][1]["chaos"]["matches"][0].update(matches=0),
            lambda r: r["steps"][2]["restart"].update(boot_method="restore"),
            lambda r: r["steps"][2]["restart"].update(processing_status="queued"),
            lambda r: r["steps"][2]["restart"].update(storage_after=[]),
            lambda r: r["steps"][2]["restart"]["storage_after"][0].update(passed=False),
            lambda r: r["steps"][3].update(fault_cleared=False),
            lambda r: r["steps"][4]["load"].update(status="failed"),
            lambda r: r["steps"].pop(),
        )
        for mutate in mutations:
            with self.subTest(mutation=mutate), self.assertRaises(ValueError):
                document = receipt()
                mutate(document[0])
                native.verify_report(document, "success", 0, False)
        for document in ([], receipt() * 2):
            with self.assertRaises(ValueError):
                native.verify_report(document, "success", 0, False)

    def test_negative_cases_do_not_accept_unrelated_failure_or_missing_signal(self):
        document = receipt("failure")
        document[0]["steps"][-1]["error"] = "API credentials rejected"
        with self.assertRaises(ValueError):
            native.verify_report(document, "failure", 1, False)
        with self.assertRaises(ValueError):
            native.verify_report(receipt("cancel"), "cancel", 1, False)
        with self.assertRaises(ValueError):
            native.verify_report(receipt(), "success", 1, False)

    def test_variants_keep_real_restart_and_use_current_source(self):
        repo = Path(__file__).resolve().parents[2]
        with tempfile.TemporaryDirectory() as temporary:
            variants = native.manifests(repo, Path(temporary), "native-checkout")
            self.assertEqual(set(variants), set(native.CASES))
            for case, (path, project) in variants.items():
                suite, _, mode, _ = native.case_contract(case)
                text = path.read_text()
                self.assertIn("project: " + project, text)
                self.assertIn("engine: real-vm", text)
                if suite in ("http-restart-matrix", "http-dependency-restart"):
                    self.assertIn("in_flight:", text)
                    self.assertIn("source: " + json.dumps(str(repo / "examples/scenario-http-write/app")), text)
                    self.assertIn("provider-" + (native.DEPENDENCY_MATRIX if suite == "http-dependency-restart" else native.RESTART_MATRIX)[case][0], text)
                elif case == "isolation":
                    self.assertIn("native-isolation-acceptance.mjs, all", text)
                    self.assertIn("GREGALE_ISOLATION_FIXTURE", text)
                    self.assertIn("inventory:", text)
                elif case == "repeated":
                    self.assertIn("native-repeated-acceptance.mjs, all", text)
                    self.assertIn("GREGALE_COMBINED_FIXTURE", text)
                elif case == "combined":
                    self.assertIn("native-combined-acceptance.mjs, all", text)
                    self.assertIn("GREGALE_COMBINED_FIXTURE", text)
                elif case == "bulkhead":
                    self.assertIn("native-bulkhead-acceptance.mjs, all", text)
                    self.assertIn("source: " + json.dumps(str(repo / "examples/scenario-http-write/native-bulkhead-app")), text)
                elif case == "circuit-breaker":
                    self.assertIn("native-circuit-breaker-acceptance.mjs, all", text)
                    self.assertIn("source: " + json.dumps(str(repo / "examples/scenario-http-write/native-breaker-app")), text)
                elif case == "retry-budget":
                    self.assertIn("native-retry-budget-acceptance.mjs, all", text)
                    self.assertIn("source: " + json.dumps(str(repo / "examples/scenario-http-write/native-retry-app")), text)
                elif case == "ssl-request":
                    self.assertIn("native-ssl-request-acceptance.mjs, all", text)
                    self.assertIn("GREGALE_NATIVE_SSL_REQUEST", text)
                elif case == "read-cut":
                    self.assertIn("native-read-cut-acceptance.mjs, all", text)
                    self.assertIn("GREGALE_NATIVE_READ_CUT", text)
                elif case == "bandwidth":
                    self.assertIn("native-bandwidth-acceptance.mjs, all", text)
                    self.assertIn("GREGALE_NATIVE_BANDWIDTH", text)
                elif case.startswith("commit-"):
                    self.assertIn("native-commit-acceptance.mjs, all", text)
                    self.assertIn("GREGALE_NATIVE_COMMIT_LOSS", text)
                elif case == "auth-stall":
                    self.assertIn("GREGALE_NATIVE_AUTH_KEY_PEM", text)
                    self.assertIn("native-auth-acceptance.mjs, all", text)
                    self.assertIn("source: " + json.dumps(str(repo / "examples/scenario-http-write/native-auth-app")), text)
                elif case == "tcp-flight":
                    self.assertIn("GREGALE_POOL_CANCEL_FIXTURE", text)
                    self.assertIn("native-pool-cancel.mjs", text)
                    self.assertIn("min_matches: 2", text)
                elif case in native.TRANSPORT_CASES:
                    self.assertIn("source: " + json.dumps(str(repo / "examples/scenario-checkout/native-probe")), text)
                    self.assertIn("from: native-checkout-" + case, text)
                    self.assertEqual("await-cancellation" in text, mode == "cancel")
                elif case in native.STRICT_CASES:
                    self.assertIn("per_request: true", text)
                    self.assertIn("GREGALE_REQUEST_ATTEMPTS_MODE", text)
                    self.assertIn("from: native-checkout-" + case, text)
                elif suite == "pending-recovery":
                    self.assertIn("source: " + json.dumps(str(repo / "examples/scenario-checkout/native-probe")), text)
                    self.assertIn('PROBE_DELAY_MS: "15000"' if mode == "stuck" else 'PROBE_DELAY_MS: "25"', text)
                    self.assertIn("max_pending_age: 500ms", text)
                else:
                    self.assertIn("restart-checkout", text)
                    self.assertIn("source: " + json.dumps(str(repo / "examples/scenario-checkout/app")), text)
                    self.assertEqual("await-cancellation" in text, mode == "cancel")
                    self.assertEqual("process.exit(42)" in text, mode == "failure")
                    if suite == "checkout-batch":
                        self.assertIn("customer-5-pending", text)
                        self.assertIn("customer-5-confirmed", text)
        with tempfile.TemporaryDirectory() as temporary, self.assertRaises(ValueError):
            native.manifests(repo, Path(temporary), "../../unsafe")

    def test_batch_requires_each_customer_and_replacement_proof(self):
        mutations = (
            lambda r: r["steps"][1]["chaos"]["matches"][0].update(matches=5),
            lambda r: r["steps"][1]["requests"].pop(),
            lambda r: r["steps"][2]["restart"]["storage_after"].pop(),
            lambda r: r["steps"][2]["restart"]["storage_before"][2].update(passed=False),
            lambda r: r["steps"][4]["requests"][1].update(passed=False),
            lambda r: r["steps"][4]["requests"].pop(),
        )
        for mutate in mutations:
            with self.subTest(mutation=mutate), self.assertRaises(ValueError):
                document = receipt("batch-success")
                mutate(document[0])
                native.verify_report(document, "batch-success", 0, False)
        with self.assertRaises(ValueError):
            native.verify_report(receipt("batch-cancel"), "batch-cancel", 1, False)

    def test_pending_failure_requires_healthy_completed_traffic_and_overdue_request(self):
        mutations = (
            lambda r: r["steps"][1]["load"]["chaos_phases"][1]["chaos"]["matches"][0].update(matches=0),
            lambda r: r["steps"][1]["load"]["thresholds"][0].update(passed=False),
            lambda r: r["steps"][1]["load"].update(streams=[]),
            lambda r: r["steps"][1]["load"]["chaos_phases"][2]["recovery"].update(overdue_requests=0),
            lambda r: r["steps"][1]["load"]["chaos_phases"][2]["recovery"].update(max_pending_age_ms=0),
            lambda r: r["steps"][1]["load"]["chaos_phases"][2]["recovery"].update(thresholds=[]),
            lambda r: r["steps"][1]["load"]["chaos_phases"][2]["recovery"]["thresholds"][0].update(actual=2000),
            lambda r: r["steps"][1]["load"]["chaos_phases"][2]["recovery"].update(error="latency budget failed"),
            lambda r: r["steps"][1]["load"]["chaos_phases"][2].update(requests=0),
            lambda r: r["steps"][1]["load"]["chaos_phases"][2].update(actual_duration_ms=0),
            lambda r: r["steps"][1].update(error="API unavailable"),
        )
        for mutate in mutations:
            with self.subTest(mutation=mutate), self.assertRaises(ValueError):
                document = receipt("pending-stuck")
                mutate(document[0])
                native.verify_report(document, "pending-stuck", 1, False)
        with self.assertRaises(ValueError):
            native.verify_report(receipt("pending-healthy"), "pending-stuck", 0, False)
        with self.assertRaises(ValueError):
            native.verify_report(receipt("pending-stuck"), "pending-stuck", 1, True)
        document = receipt("pending-healthy")
        document[0]["steps"][1]["load"]["chaos_phases"][2]["recovery"]["recovery_ms"] = 2001
        with self.assertRaises(ValueError):
            native.verify_report(document, "pending-healthy", 0, False)

    def test_verdict_requires_all_runs_and_probes_cleanup_on_rejection(self):
        repo = Path(__file__).resolve().parents[2]
        for variant in ("all", "incomplete", "previous-gate", "transport-gate", "invalid"):
            with self.subTest(variant=variant), tempfile.TemporaryDirectory() as temporary:
                directory = Path(temporary)
                cli = directory / "gregale"
                cli.write_text("candidate")
                manifests = native.manifests(repo, directory, "native-checkout")
                if variant == "incomplete":
                    manifests = {case: manifests[case] for case in native.MODES}
                elif variant == "previous-gate":
                    manifests = {case: value for case, value in manifests.items() if case not in native.TRANSPORT_CASES and case != "tcp-flight"}
                elif variant == "transport-gate":
                    manifests.pop("tcp-flight")
                def execute(command, log, marker=None, environment=None, checkpoint=None):
                    case = Path(command[command.index("--manifest") + 1]).stem
                    document = receipt(case)
                    if variant == "invalid":
                        document[0]["engine"] = "simulated"
                    Path(command[command.index("--report") + 1]).write_text(json.dumps(document))
                    Path(command[command.index("--html") + 1]).write_text("<html>evidence</html>")
                    _, _, mode, _ = native.case_contract(case)
                    suite = ET.Element("testsuite", tests="1")
                    testcase = ET.SubElement(suite, "testcase")
                    if mode != "success":
                        ET.SubElement(testcase, "failure")
                    ET.SubElement(testcase, "system-out").text = json.dumps(document[0])
                    ET.ElementTree(suite).write(command[command.index("--junit") + 1])
                    if case == "isolation":
                        (Path(log).parent / "isolation-proof.json").write_text(json.dumps(isolation_proof(document[0])))
                    if case == "repeated":
                        (Path(log).parent / "repeated-proof.json").write_text(json.dumps(repeated_proof(document[0])))
                    if case == "combined":
                        (Path(log).parent / "combined-proof.json").write_text(json.dumps(combined_proof(document[0])))
                    if case == "bulkhead":
                        (Path(log).parent / "bulkhead-proof.json").write_text(json.dumps(bulkhead_proof(document[0])))
                    if case == "circuit-breaker":
                        (Path(log).parent / "circuit-breaker-proof.json").write_text(json.dumps(circuit_breaker_proof(document[0])))
                    if case == "retry-budget":
                        (Path(log).parent / "retry-budget-proof.json").write_text(json.dumps(retry_budget_proof(document[0])))
                    if case == "ssl-request":
                        (Path(log).parent / "ssl-request-proof.json").write_text(json.dumps(ssl_request_proof(document[0])))
                    if case == "read-cut":
                        (Path(log).parent / "read-cut-proof.json").write_text(json.dumps(read_cut_proof(document[0])))
                    if case == "bandwidth":
                        (Path(log).parent / "bandwidth-proof.json").write_text(json.dumps(bandwidth_proof(document[0])))
                    if case.startswith("commit-"):
                        (Path(log).parent / "commit-proof.json").write_text(json.dumps(commit_proof(document[0], "drop" if case == "commit-loss" else case.removeprefix("commit-") + "-deadline")))
                    if case == "auth-stall":
                        (Path(log).parent / "auth-proof.json").write_text(json.dumps(auth_proof(document[0])))
                    if case == "tcp-flight":
                        self.assertIsNotNone(checkpoint)
                        (Path(log).parent / "pool-cancel-proof.json").write_text(json.dumps(pool_cancel_proof(document[0], marker)))
                    return (0 if mode == "success" else 1), bool(marker)
                with contextlib.ExitStack() as stack:
                    stack.enter_context(patch.object(sys, 'argv', ['native', '--repo', str(repo), '--out', str(directory), '--cli', str(cli)]))
                    stack.enter_context(patch.object(native, 'runner_configuration', return_value=(object(), {'native_runner': True})))
                    stack.enter_context(patch.object(native, 'manifests', return_value=manifests))
                    stack.enter_context(patch.object(native.subprocess, 'run'))
                    stack.enter_context(patch.object(native.subprocess, 'check_output', side_effect=lambda *args, **kwargs: 'a' * 40 if kwargs.get('text') else b''))
                    stack.enter_context(patch.object(native, 'auth_environment', side_effect=lambda case, run_dir: contextlib.nullcontext({})))
                    stack.enter_context(patch.object(native, 'verify_combined_database', return_value={'passed': True}))
                    stack.enter_context(patch.object(native, 'verify_repeated_database', return_value={'passed': True}))
                    stack.enter_context(patch.object(native, 'verify_isolation_database', return_value={'passed': True}))
                    stack.enter_context(patch.object(native, 'verify_bulkhead_database', return_value={'passed': True}))
                    stack.enter_context(patch.object(native, 'verify_circuit_breaker_database', return_value={'passed': True}))
                    stack.enter_context(patch.object(native, 'verify_retry_budget_database', return_value={'passed': True}))
                    stack.enter_context(patch.object(native, 'verify_ssl_request_database', return_value={'passed': True}))
                    stack.enter_context(patch.object(native, 'verify_read_cut_database', return_value={'passed': True}))
                    stack.enter_context(patch.object(native, 'verify_bandwidth_database', return_value={'passed': True}))
                    stack.enter_context(patch.object(native, 'verify_commit_database', return_value={'passed': True}))
                    stack.enter_context(patch.object(native, 'verify_auth_database', return_value={'passed': True, 'backend_sessions': 0, 'unretired_backends': 0}))
                    stack.enter_context(patch.object(native, 'execute', side_effect=execute))
                    cleanup = stack.enter_context(patch.object(native, 'verify_cleanup', return_value={'passed': True}))
                    database_cleanup = stack.enter_context(patch.object(native, 'verify_dependency_database', return_value={'passed': True, 'remaining': 0}))
                    stack.enter_context(contextlib.redirect_stdout(io.StringIO()))
                    if variant == "all":
                        native.main()
                    else:
                        with self.assertRaises(SystemExit):
                            native.main()
                verdict = json.loads((directory / "verdict.json").read_text())
                self.assertEqual(verdict["version"], 20)
                self.assertEqual(verdict["status"], "passed" if variant == "all" else "failed")
                self.assertEqual(cleanup.call_count, {"all": 38, "transport-gate": 37, "incomplete": 3, "previous-gate": 30, "invalid": 1}[variant])
                self.assertEqual(database_cleanup.call_count, 2 if variant in ("all", "previous-gate", "transport-gate") else 0)
                if variant == "invalid":
                    self.assertFalse(verdict["runs"]["success"]["verified"])
                    self.assertTrue(verdict["runs"]["success"]["cleanup"]["passed"])

    def test_commit_loss_requires_positive_fault_safe_replay_and_independent_database(self):
        report = receipt("commit-loss")[0]
        good = commit_proof(report)
        self.assertTrue(native.verify_commit_proof(good, report)["passed"])
        for stage, field, value in (("unknown", "attempts", 2), ("unknown", "acknowledgements_dropped", 0), ("unknown", "tls_connections_verified", 0), ("replay", "commits_forwarded", 3), ("cleanup", "connections", 1), ("cleanup", "stopped", False), ("replay", "instance_id", str(uuid.uuid4()))):
            altered = json.loads(json.dumps(good)); altered[stage]["status"][field] = value
            with self.subTest(stage=stage, field=field), self.assertRaises(ValueError):
                native.verify_commit_proof(altered, report)
        for stage, field, value in (("unknown", "database", {}), ("replay", "conflict", {"status": 200}), ("cleanup", "retirement", {}), ("cleanup", "database", {"remaining": 1})):
            altered = json.loads(json.dumps(good)); altered[stage][field] = value
            with self.subTest(stage=stage, field=field), self.assertRaises(ValueError):
                native.verify_commit_proof(altered, report)

    def test_commit_deadlines_require_cancellation_timing_and_distinct_database_outcomes(self):
        for direction in ("request", "response"):
            mode = direction + "-deadline"
            report = receipt("commit-" + direction)[0]
            good = commit_proof(report, mode)
            self.assertTrue(native.verify_commit_proof(good, report, mode)["passed"])
            for field, value in (("elapsed_ms", 5000), ("deadline_ms", 5000), ("database", {"charges": 1}), ("response", {"status": 200})):
                altered = json.loads(json.dumps(good)); altered["unknown"][field] = value
                with self.subTest(direction=direction, field=field), self.assertRaises(ValueError):
                    native.verify_commit_proof(altered, report, mode)
            for field, value in (("outcome", "timed_out"), ("elapsed_ms", 100), ("direction", "response" if direction == "request" else "request")):
                altered = json.loads(json.dumps(good)); altered["unknown"]["status"]["faults"][0][field] = value
                with self.subTest(direction=direction, field=field), self.assertRaises(ValueError):
                    native.verify_commit_proof(altered, report, mode)

    def test_bandwidth_requires_complete_reads_positive_pacing_and_joined_cancellation(self):
        report = receipt("bandwidth")[0]; good = bandwidth_proof(report)
        self.assertTrue(native.verify_bandwidth_proof(good, report)["passed"])
        for stage, field, value in (("limited", "response", {"status": 200}), ("timeout", "response", {"status": 200}), ("timeout", "elapsed_ms", 5000), ("cleanup", "database", {})):
            altered = json.loads(json.dumps(good)); altered[stage][field] = value
            with self.subTest(stage=stage, field=field), self.assertRaises(ValueError):
                native.verify_bandwidth_proof(altered, report)
        for field, value in (("bytes_forwarded", 0), ("canceled_transfers", 0), ("outcome", "cleared"), ("elapsed_ms", 1), ("bytes_per_second", 262144), ("direction", "request")):
            altered = json.loads(json.dumps(good)); altered["timeout"]["status"]["bandwidth"][1][field] = value
            with self.subTest(field=field), self.assertRaises(ValueError):
                native.verify_bandwidth_proof(altered, report)
        altered = json.loads(json.dumps(good)); altered["cleanup"]["status"]["bandwidth_buffered_bytes"] = 1
        with self.assertRaises(ValueError):
            native.verify_bandwidth_proof(altered, report)

    def test_read_cut_requires_truncated_row_concurrent_isolation_and_joined_resources(self):
        report = receipt("read-cut")[0]; good = read_cut_proof(report)
        self.assertTrue(native.verify_read_cut_proof(good, report)["passed"])
        for stage, field, value in (("interrupted", "responses", [{"status": 200}]), ("concurrent", "responses", [{"status": 503}, {"status": 503}]), ("cleanup", "database", {}), ("final", "elapsed_ms", 5000)):
            altered = json.loads(json.dumps(good)); altered[stage][field] = value
            with self.subTest(stage=stage, field=field), self.assertRaises(ValueError):
                native.verify_read_cut_proof(altered, report)
        for field, value in (("read_cuts_fired", 0), ("concurrent_read_pairs", 0), ("read_pair_timers", 1), ("read_cut_armed", True), ("attempts", 6), ("tls_connections_verified", 0)):
            altered = json.loads(json.dumps(good)); altered["concurrent"]["status"][field] = value
            with self.subTest(field=field), self.assertRaises(ValueError):
                native.verify_read_cut_proof(altered, report)
        altered = json.loads(json.dumps(good)); altered["interrupted"]["status"]["read_cuts"][0]["prefix_flushed_bytes"] = 0
        with self.assertRaises(ValueError):
            native.verify_read_cut_proof(altered, report)

    def test_ssl_request_requires_pre_tls_timeout_bounded_queue_and_verified_recovery(self):
        report = receipt("ssl-request")[0]; good = ssl_request_proof(report)
        self.assertTrue(native.verify_ssl_request_proof(good, report)["passed"])
        for stage, field, value in (("retries", "connection_attempts", 5), ("queued", "peak_pool_waiting", 0), ("queued", "connection_stalls_fired", 7), ("queued", "authentication_challenges_seen", 2), ("queued", "tls_connections_verified", 2), ("cleanup", "connection_stall_timers", 1), ("recovery", "tls_connections_verified", 1)):
            altered = json.loads(json.dumps(good)); altered[stage]["status"][field] = value
            with self.subTest(stage=stage, field=field), self.assertRaises(ValueError):
                native.verify_ssl_request_proof(altered, report)
        for field, value in (("phase", "authentication"), ("outcome", "timed_out"), ("elapsed_ms", 5000)):
            altered = json.loads(json.dumps(good)); altered["retries"]["status"]["connection_stalls"][0][field] = value
            with self.subTest(field=field), self.assertRaises(ValueError):
                native.verify_ssl_request_proof(altered, report)
        altered = json.loads(json.dumps(good)); altered["queued"]["responses"] = [{"status": 200}]*3
        with self.assertRaises(ValueError):
            native.verify_ssl_request_proof(altered, report)

    def test_retry_budget_requires_shared_allowance_deadlines_cancellation_and_recovery(self):
        report = receipt("retry-budget")[0]; good = retry_budget_proof(report)
        self.assertTrue(native.verify_retry_budget_proof(good, report)["passed"])
        for stage, field, value in (("outage", "retries_admitted", 6), ("outage", "remaining", 1), ("outage", "window_id", 2), ("cancellation", "retries_admitted", 1), ("cleanup", "retry_timers", 1), ("cleanup", "deadline_timers", 1), ("recovery", "dependency_attempts", 14)):
            altered = json.loads(json.dumps(good)); altered[stage]["status"]["caller"][field] = value
            with self.subTest(stage=stage, field=field), self.assertRaises(ValueError):
                native.verify_retry_budget_proof(altered, report)
        altered = json.loads(json.dumps(good)); altered["outage"]["responses"][0]["body"]["attempts"] = 2
        with self.assertRaises(ValueError):
            native.verify_retry_budget_proof(altered, report)
        altered = json.loads(json.dumps(good)); altered["cancellation"]["client_aborted"] = False
        with self.assertRaises(ValueError):
            native.verify_retry_budget_proof(altered, report)

    def test_circuit_breaker_requires_open_fail_fast_single_probe_reopen_and_recovery(self):
        report = receipt("circuit-breaker")[0]; good = circuit_breaker_proof(report)
        self.assertTrue(native.verify_circuit_breaker_proof(good, report)["passed"])
        for stage, field, value in (("open", "dependency_attempts", 9), ("failed_probe", "peak_probes", 4), ("failed_probe", "open_transitions", 1), ("cancellation", "probe_cancellations", 0), ("recovery", "state", "open"), ("cleanup", "deadline_timers", 1)):
            altered = json.loads(json.dumps(good)); altered[stage]["status"]["caller"][field] = value
            with self.subTest(stage=stage, field=field), self.assertRaises(ValueError):
                native.verify_circuit_breaker_proof(altered, report)
        for stage, field, value in (("open", "elapsed_ms", 2000), ("cancellation", "client_aborted", False), ("recovery", "responses", [])):
            altered = json.loads(json.dumps(good)); altered[stage][field] = value
            with self.subTest(stage=stage, field=field), self.assertRaises(ValueError):
                native.verify_circuit_breaker_proof(altered, report)
        altered = json.loads(json.dumps(good)); altered["failed_probe"]["observed"]["active_probes"] = 0
        with self.assertRaises(ValueError):
            native.verify_circuit_breaker_proof(altered, report)

    def test_bulkhead_requires_bounded_permits_queue_healthy_route_and_cleanup(self):
        report = receipt("bulkhead")[0]; good = bulkhead_proof(report)
        self.assertTrue(native.verify_bulkhead_proof(good, report)["passed"])
        for stage, field, value in (("outage", "peak_active", 6), ("outage", "peak_queue", 4), ("outage", "dependency_attempts", 9), ("cancellation", "queue_cancellations", 0), ("cleanup", "permits_released", 6), ("cleanup", "queue_timers", 1)):
            altered = json.loads(json.dumps(good)); altered[stage]["status"]["caller"][field] = value
            with self.subTest(stage=stage, field=field), self.assertRaises(ValueError):
                native.verify_bulkhead_proof(altered, report)
        altered = json.loads(json.dumps(good)); altered["outage"]["health"][0]["body"]["active_dependencies"] = 0
        with self.assertRaises(ValueError):
            native.verify_bulkhead_proof(altered, report)
        altered = json.loads(json.dumps(good)); altered["outage"]["responses"][4]["elapsed_ms"] = 2000
        with self.assertRaises(ValueError):
            native.verify_bulkhead_proof(altered, report)

    def test_isolation_receipt_requires_both_hot_providers_and_all_workload_ids(self):
        good = receipt("isolation")
        self.assertEqual(native.verify_report(good,"isolation",0,False),good[0])
        for mutation in (lambda r: r["service_hot"].pop("inventory"), lambda r: r["workload_app_ids"].pop("inventory"), lambda r: r["requests"].pop(), lambda r: r["service_hot"]["inventory"].update(no_new_wake=False)):
            altered = copy.deepcopy(good); mutation(altered[0])
            with self.assertRaises(ValueError): native.verify_report(altered,"isolation",0,False)
        class FakeAPI:
            paths = []
            def get(self,path,timeout=10):
                self.paths.append(path)
                return (200,{"instances":[]}) if path.startswith("/v1/instances") else (404,None)
        api = FakeAPI(); native.cleanup_probe(api,good[0],"native-checkout-isolation")
        self.assertTrue(any(path.startswith("/v1/dev/sessions/native-checkout-isolation-inventory?") for path in api.paths))

    def test_isolation_rejects_shared_policies_slow_healthy_calls_and_unretired_resources(self):
        report = receipt("isolation")[0]; good = isolation_proof(report)
        self.assertTrue(native.verify_isolation_proof(good,report)["passed"])
        for field, value in (("remaining",0),("window_id",1),("state","open"),("active_dependencies",1),("permits_released",0)):
            altered = copy.deepcopy(good)
            altered["phases"][0]["outage"]["status"]["caller"]["dependencies"]["b"][field] = value
            with self.assertRaises(ValueError): native.verify_isolation_proof(altered,report)
        for field, value in (("elapsed_ms",750),("status",503)):
            altered = copy.deepcopy(good)
            altered["phases"][1]["outage"]["healthy_responses"][0][field] = value
            with self.assertRaises(ValueError): native.verify_isolation_proof(altered,report)
        altered = copy.deepcopy(good); altered["provider_ids"]["b"] = altered["provider_ids"]["a"]
        with self.assertRaises(ValueError): native.verify_isolation_proof(altered,report)
        altered = copy.deepcopy(good); altered["phases"][0]["after_healthy"]["dependencies"]["a"]["active_dependencies"] = 0
        with self.assertRaises(ValueError): native.verify_isolation_proof(altered,report)
        altered = copy.deepcopy(good); altered["cleanup"]["status"]["providers"]["b"]["pool_total"] = 1
        with self.assertRaises(ValueError): native.verify_isolation_proof(altered,report)
        altered = copy.deepcopy(good); altered["phases"].pop()
        with self.assertRaises(ValueError): native.verify_isolation_proof(altered,report)

    def test_repeated_rejects_resource_accumulation_early_refill_and_missing_cycles(self):
        report = receipt("repeated")[0]
        good = repeated_proof(report)
        self.assertTrue(native.verify_repeated_proof(good, report)["passed"])
        for field, value in (("active_dependencies",1),("permits_released",0),("window_id",1)):
            altered = copy.deepcopy(good)
            altered["cycles"][1]["outage"]["status"]["caller"][field] = value
            with self.assertRaises(ValueError):
                native.verify_repeated_proof(altered,report)
        altered = copy.deepcopy(good)
        altered["cycles"][1]["expiry"]["after"]["retry_window_age_ms"] = 29999
        with self.assertRaises(ValueError):
            native.verify_repeated_proof(altered,report)
        altered = copy.deepcopy(good); altered["cycles"].pop()
        with self.assertRaises(ValueError):
            native.verify_repeated_proof(altered,report)

    def test_combined_requires_protected_retries_breaker_probe_limit_and_joined_bulkhead(self):
        report = receipt("combined")[0]; good = combined_proof(report)
        self.assertTrue(native.verify_combined_proof(good, report)["passed"])
        for stage, field, value in (("outage", "retries_admitted", 4), ("outage", "permit_protected_attempts", 5), ("outage", "peak_active", 4), ("open", "dependency_attempts", 13), ("failed_probe", "probes_started", 3), ("cancellation", "active_probes", 1), ("cleanup", "permits_released", 11), ("cleanup", "retry_timers", 1)):
            altered = json.loads(json.dumps(good)); altered[stage]["status"]["caller"][field] = value
            with self.subTest(stage=stage, field=field), self.assertRaises(ValueError):
                native.verify_combined_proof(altered, report)
        altered = json.loads(json.dumps(good)); altered["failed_probe"]["observed"]["active_dependencies"] = 0
        with self.assertRaises(ValueError):
            native.verify_combined_proof(altered, report)
        altered = json.loads(json.dumps(good)); altered["outage"]["health"][0]["body"]["active_dependencies"] = 0
        with self.assertRaises(ValueError):
            native.verify_combined_proof(altered, report)

    def test_authentication_requires_positive_bound_fault_and_independent_retirement(self):
        report = receipt("auth-stall")[0]
        good = auth_proof(report)
        self.assertTrue(native.verify_auth_proof(good, report)["passed"])
        for field, value in (("connection_stalls_fired", 0), ("tls_connections_verified", 0), ("attempts", 99), ("pending_authentication", 1), ("connection_stall_enabled", True)):
            altered = json.loads(json.dumps(good))
            altered["recovery"]["status"][field] = value
            with self.subTest(field=field), self.assertRaises(ValueError):
                native.verify_auth_proof(altered, report)
        for mutation in ("phase", "duration", "backend", "namespace", "identity", "stop"):
            altered = json.loads(json.dumps(good))
            if mutation == "phase": altered["outage"]["status"]["connection_stalls"][0]["phase"] = "ssl_request"
            elif mutation == "duration": altered["outage"]["status"]["connection_stalls"][0]["elapsed_ms"] = 0
            elif mutation == "backend": altered["cleanup"]["database"]["backend_sessions"] = 1
            elif mutation == "namespace": altered["namespace"] = uuid.uuid4().hex
            elif mutation == "identity": altered["recovery"]["status"]["instance_id"] = str(uuid.uuid4())
            else: altered["cleanup"]["status"]["stopped"] = False
            with self.subTest(mutation=mutation), self.assertRaises(ValueError):
                native.verify_auth_proof(altered, report)
        report["service_hot"]["payment"]["no_new_wake"] = False
        with self.assertRaises(ValueError):
            native.verify_report([report], "auth-stall", 0, False)

    def test_native_auth_identity_is_ephemeral_and_never_written_to_artifacts(self):
        with tempfile.TemporaryDirectory() as temporary:
            destination = Path(temporary)
            with patch.object(native.subprocess, "run", wraps=native.subprocess.run) as invoked:
                with self.assertRaises(RuntimeError):
                    with native.auth_environment("auth-stall", destination) as environment:
                        self.assertRegex(environment["GREGALE_NATIVE_AUTH_CONTROL_TOKEN"], r"^[0-9a-f]{64}$")
                        self.assertTrue(environment["GREGALE_NATIVE_AUTH_CERT_PEM"].startswith("-----BEGIN CERTIFICATE"))
                        command = invoked.call_args.args[0]
                        key_path = Path(command[command.index("-keyout") + 1])
                        self.assertTrue(key_path.is_file())
                        self.assertEqual(key_path.stat().st_mode & 0o077, 0)
                        self.assertFalse(list(destination.rglob("*.pem")))
                        raise RuntimeError("fixture failure")
                self.assertFalse(key_path.exists())

    def test_independent_auth_cleanup_rejects_unretired_or_wrong_namespace_proof(self):
        report = receipt("auth-stall")[0]
        expected = {"namespace": report["run_id"], "passed": True, "backend_sessions": 0, "unretired_backends": 0}
        for proof in (expected, {**expected, "unretired_backends": 1}, {**expected, "namespace": uuid.uuid4().hex}):
            result = type("Result", (), {"returncode": 0, "stdout": json.dumps(proof)})()
            with patch.object(native.subprocess, "run", return_value=result):
                if proof == expected:
                    self.assertEqual(native.verify_auth_database(Path("/repo"), report, Path("/proof.json")), expected)
                else:
                    with self.assertRaises(ValueError):
                        native.verify_auth_database(Path("/repo"), report, Path("/proof.json"))

    def test_independent_commit_cleanup_rejects_remaining_rows_backends_and_wrong_identity(self):
        report = receipt("commit-loss")[0]
        expected = {"namespace": report["run_id"], "passed": True, "remaining": 0, "backend_sessions": 0, "unretired_backends": 0}
        for proof in (expected, {**expected, "remaining": 1}, {**expected, "unretired_backends": 1}, {**expected, "backend_sessions": 1}, {**expected, "namespace": uuid.uuid4().hex}):
            result = type("Result", (), {"returncode": 0, "stdout": json.dumps(proof)})()
            with patch.object(native.subprocess, "run", return_value=result):
                if proof == expected:
                    self.assertEqual(native.verify_commit_database(Path("/repo"), report, Path("/proof.json")), expected)
                else:
                    with self.assertRaises(ValueError):
                        native.verify_commit_database(Path("/repo"), report, Path("/proof.json"))

    def test_native_commit_identity_is_private_and_scoped_to_its_run(self):
        with tempfile.TemporaryDirectory() as temporary:
            destination = Path(temporary)
            with native.auth_environment("commit-loss", destination) as environment:
                self.assertEqual(environment["GREGALE_NATIVE_COMMIT_PROOF"], str((destination / "commit-proof.json").resolve()))
                self.assertRegex(environment["GREGALE_NATIVE_AUTH_CONTROL_TOKEN"], r"^[0-9a-f]{64}$")
                self.assertTrue(environment["GREGALE_NATIVE_AUTH_KEY_PEM"].startswith("-----BEGIN PRIVATE KEY"))
                self.assertFalse(list(destination.rglob("*.pem")))

    def test_bandwidth_identity_and_independent_cleanup_are_bound_to_the_run(self):
        report = receipt("bandwidth")[0]
        expected = {"namespace": report["run_id"], "passed": True, "backend_sessions": 0, "unretired_backends": 0}
        for proof in (expected, {**expected, "unretired_backends": 1}, {**expected, "namespace": uuid.uuid4().hex}):
            result = type("Result", (), {"returncode": 0, "stdout": json.dumps(proof)})()
            with patch.object(native.subprocess, "run", return_value=result):
                if proof == expected:
                    self.assertEqual(native.verify_bandwidth_database(Path("/repo"), report, Path("/proof.json")), expected)
                else:
                    with self.assertRaises(ValueError):
                        native.verify_bandwidth_database(Path("/repo"), report, Path("/proof.json"))
        with tempfile.TemporaryDirectory() as temporary:
            destination = Path(temporary)
            with native.auth_environment("bandwidth", destination) as environment:
                self.assertEqual(environment["GREGALE_NATIVE_BANDWIDTH_PROOF"], str((destination / "bandwidth-proof.json").resolve()))
                self.assertFalse(list(destination.rglob("*.pem")))

    def test_read_cut_private_identity_and_independent_cleanup(self):
        report = receipt("read-cut")[0]
        expected = {"namespace": report["run_id"], "passed": True, "backend_sessions": 0, "unretired_backends": 0}
        for proof in (expected, {**expected, "unretired_backends": 1}, {**expected, "namespace": uuid.uuid4().hex}):
            result = type("Result", (), {"returncode": 0, "stdout": json.dumps(proof)})()
            with patch.object(native.subprocess, "run", return_value=result):
                if proof == expected:
                    self.assertEqual(native.verify_read_cut_database(Path("/repo"), report, Path("/proof.json")), expected)
                else:
                    with self.assertRaises(ValueError):
                        native.verify_read_cut_database(Path("/repo"), report, Path("/proof.json"))
        with tempfile.TemporaryDirectory() as temporary:
            destination = Path(temporary)
            with native.auth_environment("read-cut", destination) as environment:
                self.assertEqual(environment["GREGALE_NATIVE_READ_CUT_PROOF"], str((destination / "read-cut-proof.json").resolve()))
                self.assertFalse(list(destination.rglob("*.pem")))

    def test_ssl_request_identity_and_independent_cleanup(self):
        report = receipt("ssl-request")[0]
        expected = {"namespace": report["run_id"], "passed": True, "backend_sessions": 0, "unretired_backends": 0}
        for proof in (expected, {**expected, "backend_sessions": 1}, {**expected, "unretired_backends": 1}, {**expected, "namespace": uuid.uuid4().hex}):
            result = type("Result", (), {"returncode": 0, "stdout": json.dumps(proof)})()
            with patch.object(native.subprocess, "run", return_value=result):
                if proof == expected:
                    self.assertEqual(native.verify_ssl_request_database(Path("/repo"), report, Path("/proof.json")), expected)
                else:
                    with self.assertRaises(ValueError):
                        native.verify_ssl_request_database(Path("/repo"), report, Path("/proof.json"))
        with tempfile.TemporaryDirectory() as temporary:
            destination = Path(temporary)
            with native.auth_environment("ssl-request", destination) as environment:
                self.assertEqual(environment["GREGALE_NATIVE_SSL_REQUEST_PROOF"], str((destination / "ssl-request-proof.json").resolve()))
                self.assertFalse(list(destination.rglob("*.pem")))

    def test_retry_budget_private_identity_and_independent_cleanup(self):
        report = receipt("retry-budget")[0]
        expected = {"namespace": report["run_id"], "passed": True, "backend_sessions": 0, "unretired_backends": 0}
        for proof in (expected, {**expected, "unretired_backends": 1}, {**expected, "namespace": uuid.uuid4().hex}):
            result = type("Result", (), {"returncode": 0, "stdout": json.dumps(proof)})()
            with patch.object(native.subprocess, "run", return_value=result):
                if proof == expected:
                    self.assertEqual(native.verify_retry_budget_database(Path("/repo"), report, Path("/proof.json")), expected)
                else:
                    with self.assertRaises(ValueError):
                        native.verify_retry_budget_database(Path("/repo"), report, Path("/proof.json"))
        with tempfile.TemporaryDirectory() as temporary:
            destination = Path(temporary)
            with native.auth_environment("retry-budget", destination) as environment:
                self.assertEqual(environment["GREGALE_NATIVE_RETRY_BUDGET_PROOF"], str((destination / "retry-budget-proof.json").resolve()))
                self.assertFalse(list(destination.rglob("*.pem")))

    def test_circuit_breaker_private_identity_and_independent_cleanup(self):
        report = receipt("circuit-breaker")[0]
        expected = {"namespace": report["run_id"], "passed": True, "backend_sessions": 0, "unretired_backends": 0}
        for proof in (expected, {**expected, "unretired_backends": 1}, {**expected, "namespace": uuid.uuid4().hex}):
            result = type("Result", (), {"returncode": 0, "stdout": json.dumps(proof)})()
            with patch.object(native.subprocess, "run", return_value=result):
                if proof == expected:
                    self.assertEqual(native.verify_circuit_breaker_database(Path("/repo"), report, Path("/proof.json")), expected)
                else:
                    with self.assertRaises(ValueError):
                        native.verify_circuit_breaker_database(Path("/repo"), report, Path("/proof.json"))
        with tempfile.TemporaryDirectory() as temporary:
            destination = Path(temporary)
            with native.auth_environment("circuit-breaker", destination) as environment:
                self.assertEqual(environment["GREGALE_NATIVE_CIRCUIT_BREAKER_PROOF"], str((destination / "circuit-breaker-proof.json").resolve()))
                self.assertFalse(list(destination.rglob("*.pem")))

    def test_bulkhead_private_identity_and_independent_cleanup(self):
        report = receipt("bulkhead")[0]
        expected = {"namespace": report["run_id"], "passed": True, "backend_sessions": 0, "unretired_backends": 0}
        for proof in (expected, {**expected, "unretired_backends": 1}, {**expected, "namespace": uuid.uuid4().hex}):
            result = type("Result", (), {"returncode": 0, "stdout": json.dumps(proof)})()
            with patch.object(native.subprocess, "run", return_value=result):
                if proof == expected:
                    self.assertEqual(native.verify_bulkhead_database(Path("/repo"), report, Path("/proof.json")), expected)
                else:
                    with self.assertRaises(ValueError):
                        native.verify_bulkhead_database(Path("/repo"), report, Path("/proof.json"))
        with tempfile.TemporaryDirectory() as temporary:
            destination = Path(temporary)
            with native.auth_environment("bulkhead", destination) as environment:
                self.assertEqual(environment["GREGALE_NATIVE_BULKHEAD_PROOF"], str((destination / "bulkhead-proof.json").resolve()))
                self.assertFalse(list(destination.rglob("*.pem")))

    def test_combined_private_identity_and_independent_cleanup(self):
        report = receipt("combined")[0]
        expected = {"namespace": report["run_id"], "passed": True, "backend_sessions": 0, "unretired_backends": 0}
        for proof in (expected, {**expected, "unretired_backends": 1}, {**expected, "namespace": uuid.uuid4().hex}):
            result = type("Result", (), {"returncode": 0, "stdout": json.dumps(proof)})()
            with patch.object(native.subprocess, "run", return_value=result):
                if proof == expected:
                    self.assertEqual(native.verify_combined_database(Path("/repo"), report, Path("/proof.json")), expected)
                else:
                    with self.assertRaises(ValueError):
                        native.verify_combined_database(Path("/repo"), report, Path("/proof.json"))
        with tempfile.TemporaryDirectory() as temporary:
            destination = Path(temporary)
            with native.auth_environment("combined", destination) as environment:
                self.assertEqual(environment["GREGALE_NATIVE_COMBINED_PROOF"], str((destination / "combined-proof.json").resolve()))
                self.assertFalse(list(destination.rglob("*.pem")))

    def test_repeated_private_identity_and_independent_cleanup(self):
        report = receipt("repeated")[0]
        expected = {"namespace": report["run_id"], "passed": True, "backend_sessions": 0, "unretired_backends": 0}
        for proof in (expected, {**expected, "unretired_backends": 1}, {**expected, "namespace": uuid.uuid4().hex}):
            result = type("Result", (), {"returncode": 0, "stdout": json.dumps(proof)})()
            with patch.object(native.subprocess, "run", return_value=result):
                if proof == expected:
                    self.assertEqual(native.verify_repeated_database(Path("/repo"), report, Path("/proof.json")), expected)
                else:
                    with self.assertRaises(ValueError):
                        native.verify_repeated_database(Path("/repo"), report, Path("/proof.json"))
        with tempfile.TemporaryDirectory() as temporary:
            destination = Path(temporary)
            with native.auth_environment("repeated", destination) as environment:
                self.assertEqual(environment["GREGALE_NATIVE_REPEATED_PROOF"], str((destination / "repeated-proof.json").resolve()))
                self.assertFalse(list(destination.rglob("*.pem")))

    def test_isolation_private_identity_and_independent_cleanup(self):
        report = receipt("isolation")[0]
        expected = {"namespace": report["run_id"], "passed": True, "backend_sessions": 0, "unretired_backends": 0}
        for proof in (expected, {**expected, "unretired_backends": 1}, {**expected, "namespace": uuid.uuid4().hex}):
            result = type("Result", (), {"returncode": 0, "stdout": json.dumps(proof)})()
            with patch.object(native.subprocess, "run", return_value=result):
                if proof == expected:
                    self.assertEqual(native.verify_isolation_database(Path("/repo"), report, Path("/proof.json")), expected)
                else:
                    with self.assertRaises(ValueError):
                        native.verify_isolation_database(Path("/repo"), report, Path("/proof.json"))
        with tempfile.TemporaryDirectory() as temporary:
            destination = Path(temporary)
            with native.auth_environment("isolation", destination) as environment:
                self.assertEqual(environment["GREGALE_NATIVE_ISOLATION_PROOF"], str((destination / "isolation-proof.json").resolve()))
                self.assertFalse(list(destination.rglob("*.pem")))

    def test_complete_inventory_and_api_retirement_required(self):
        report = receipt()[0]
        owned = next(iter(report["workload_app_ids"].values()))
        class FakeAPI:
            live = False
            namespace = False
            sessions = False
            bad_auth = False
            pages = 0
            def get(self, path, timeout=10):
                if self.bad_auth:
                    return 403, None
                if path.startswith("/v1/instances"):
                    self.pages += 1
                    if "before=page2" not in path:
                        return 200, {"instances": [{"app_id": "unrelated"}], "next_before": "page2"}
                    return 200, {"instances": [{"app_id": owned}] if self.live else []}
                if "/dev/sessions/" in path:
                    return (200 if self.sessions else 404), None
                return (200 if self.namespace else 404), None
        for field in (None, "live", "namespace", "sessions"):
            api = FakeAPI()
            if field:
                setattr(api, field, True)
            result = native.cleanup_probe(api, report, "native-checkout-success")
            self.assertEqual(result["passed"], field is None)
            self.assertEqual(api.pages, 2)
        api = FakeAPI()
        api.bad_auth = True
        with self.assertRaises(ValueError):
            native.cleanup_probe(api, report, "native-checkout-success")

    def test_junit_must_contain_matching_non_skipped_receipt(self):
        document = receipt()
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "results.html").write_text("<html>native evidence</html>")
            suite = ET.Element("testsuite", tests="1")
            case = ET.SubElement(suite, "testcase")
            out = ET.SubElement(case, "system-out")
            out.text = json.dumps(document[0])
            ET.ElementTree(suite).write(root / "results.xml")
            native.verify_artifacts(root, document, "success")
            ET.SubElement(case, "skipped")
            ET.ElementTree(suite).write(root / "results.xml")
            with self.assertRaises(ValueError):
                native.verify_artifacts(root, document, "success")

    def test_execute_signals_only_after_nonce_and_waits_for_child_cleanup(self):
        marker = uuid.uuid4().hex
        code = """import os, signal, time
def stop(sig, frame):
 os.write(1, b'child-cleanup-complete\\n')
 raise SystemExit(1)
signal.signal(signal.SIGINT, stop)
os.write(1, (os.environ['GREGALE_NATIVE_CANCEL_MARKER'] + '\\n').encode())
while True: time.sleep(1)
"""
        with tempfile.TemporaryDirectory() as temporary:
            log = Path(temporary) / "run.log"
            result, issued = native.execute([sys.executable, "-c", code], log, marker, deadline_seconds=5, cleanup_seconds=2)
            self.assertEqual(result, 1)
            self.assertTrue(issued)
            self.assertIn("child-cleanup-complete", log.read_text())

    def test_deadline_is_a_failure_even_when_child_cleanup_succeeds(self):
        code = """import signal, time
def stop(sig, frame):
 print('timeout-cleanup-complete', flush=True)
 raise SystemExit(0)
signal.signal(signal.SIGINT, stop)
while True: time.sleep(1)
"""
        with tempfile.TemporaryDirectory() as temporary:
            log = Path(temporary) / "run.log"
            with self.assertRaisesRegex(ValueError, "scenario deadline exceeded"):
                native.execute([sys.executable, "-c", code], log, deadline_seconds=0.2, cleanup_seconds=2)
            self.assertIn("timeout-cleanup-complete", log.read_text())

    def test_native_guard_rejects_missing_hardware_before_using_credentials(self):
        with patch.object(native.platform, "system", return_value="Linux"), patch.object(native.platform, "machine", return_value="x86_64"), patch.object(Path, "is_char_device", return_value=False), patch.object(native.API, "get") as request:
            with self.assertRaisesRegex(ValueError, "accessible /dev/kvm"):
                native.runner_configuration()
            request.assert_not_called()


if __name__ == "__main__":
    unittest.main()
