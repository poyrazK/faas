#!/usr/bin/env python3
"""Validate native environment-promotion evidence; never enables admission."""
import argparse
from datetime import datetime, timedelta
import hashlib
import json
from pathlib import Path
import re
import uuid

SCENARIOS = {
    "TestProjectEnvironmentPromotionSuccessMetal": "succeeded",
    "TestProjectEnvironmentPromotionRollbackMetal": "rolled_back",
    "TestProjectEnvironmentPromotionControlsMetal": "operator_controls",
    "TestProjectEnvironmentPromotionConcurrentMetal": "concurrent",
    "TestProjectEnvironmentPromotionAdmissionMetal": "admission",
    "TestProjectEnvironmentPromotionMigrationSuccessMetal": "migration_success",
    "TestProjectEnvironmentPromotionMigrationRollbackMetal": "migration_rollback",
    "TestProjectEnvironmentPromotionMigrationFailureMetal": "migration_failure",
    "TestProjectEnvironmentPromotionMigrationRecoveryMetal": "migration_recovery",
    "TestProjectEnvironmentPromotionPublicationMetal": "publication_crashes",
}
META_TEST = "TestProjectEnvironmentPromotionFixtureMeta"
REQUIRED_TESTS = set(SCENARIOS) | {META_TEST}
GROUPS = {
    "promotion-core": {META_TEST, "TestProjectEnvironmentPromotionSuccessMetal", "TestProjectEnvironmentPromotionRollbackMetal"},
    "promotion-controls": {"TestProjectEnvironmentPromotionControlsMetal"},
    "promotion-load": {"TestProjectEnvironmentPromotionConcurrentMetal"},
    "promotion-admission": {"TestProjectEnvironmentPromotionAdmissionMetal"},
    "promotion-migrations": {name for name, case in SCENARIOS.items() if case.startswith("migration_")},
    "promotion-publication": {"TestProjectEnvironmentPromotionPublicationMetal"},
}
PROOFS = (
    "restart_preflight", "restart_verifying", "candidate_recovered",
    "retained_recovered", "candidate_isolated", "destination_secrets",
    "artifact_reused", "idempotent_replay", "cleanup",
)


def valid_uuid(value):
    try:
        parsed = uuid.UUID(value)
        return parsed.int != 0 and str(parsed) == value
    except (ValueError, TypeError, AttributeError):
        return False


def timestamp(value):
    if not isinstance(value, str):
        raise ValueError("timestamp must be a string")
    result = datetime.fromisoformat(value.replace("Z", "+00:00"))
    if result.tzinfo is None:
        raise ValueError("timestamp must have a timezone")
    return result


def healthy_windows(record, errors, label, after=None):
    try:
        values = record["healthy_windows"]
        if not isinstance(values, list) or len(values) < 2:
            raise ValueError("two rounds required")
        windows = [timestamp(value) for value in values]
        if any((b-a).total_seconds() < 60 for a, b in zip(windows, windows[1:])):
            raise ValueError("minute spacing required")
        if after is not None and windows[0] < timestamp(after):
            raise ValueError("evidence predates operator request")
    except (KeyError, TypeError, ValueError, OverflowError):
        errors.append(f"{label}: missing fresh minute-spaced healthy windows")


def require_proofs(record, proofs, errors, label):
    for proof in proofs:
        if record.get(proof) is not True:
            errors.append(f"{label}: missing {proof}")


def valid_request(value):
    return isinstance(value, str) and re.fullmatch(r"[a-f0-9]{32}", value) is not None


def validate_publication(record):
    errors, promotions = [], set()
    require_proofs(record, ("cleanup",), errors, "publication")
    points = ("cutover_before_commit", "cutover_after_commit", "rollback_before_commit", "rollback_after_commit")
    for case in ("recovered", "newer_intent"):
        item = record.get(case)
        if not isinstance(item, dict):
            errors.append(f"publication: missing {case}")
            continue
        newer = case == "newer_intent"
        require_proofs(item, ("candidate_pins_preserved", "completed_jobs_not_replayed", "artifact_reused", "graph_config_consistent", "terminal_restart"), errors, case)
        try:
            identity = item["promotion_id"]
            if not valid_uuid(identity) or identity in promotions:
                raise ValueError("missing independent promotion")
            promotions.add(identity)
            graphs = [item[field] for field in ("baseline_graph", "cutover_graph", "restored_graph")]
            if not all(valid_uuid(value) for value in graphs) or len(set(graphs)) != 3 or not valid_uuid(item["selected_graph"]):
                raise ValueError("invalid graph inventory")
            if newer and item["selected_graph"] in graphs or not newer and item["selected_graph"] != item["restored_graph"]:
                raise ValueError("wrong selected graph")
            if not isinstance(item["config_hash"], str) or not re.fullmatch(r"[a-f0-9]{64}", item["config_hash"]):
                raise ValueError("missing selected configuration")
            if type(item["publications"]) is not int or item["publications"] != (3 if newer else 2):
                raise ValueError("duplicated or lost publication")
            if type(item["candidate_jobs"]) is not int or item["candidate_jobs"] < 5:
                raise ValueError("missing real candidate jobs")
            if item["newer_intent_preserved"] is not newer or item["phase"] != ("rollback_failed" if newer else "failed"):
                raise ValueError("wrong recovery outcome")
            expected = points[-1:] if newer else points
            events = item["events"]
            if not isinstance(events, list) or len(events) != len(expected):
                raise ValueError("missing exact crash inventory")
            pids, releases, previous_restart = set(), set(), None
            generations, deadlines = [], []
            for point, event in zip(expected, events):
                committed = point.endswith("after_commit")
                selected = item["baseline_graph"] if point == points[0] else item["restored_graph"] if point == points[-1] else item["cutover_graph"]
                phase = "cutover" if point == points[0] else "verifying" if point == points[1] else "rolling_back"
                if not isinstance(event, dict) or event["point"] != point or event["promotion_id"] != identity or event["committed"] is not committed or event["checkpoint_preserved"] is not True:
                    raise ValueError("unattributed crash")
                if not valid_uuid(event["release_id"]) or event["release_id"] in releases or event["selected_graph"] != selected or event["phase"] != phase:
                    raise ValueError("mixed publication/checkpoint visibility")
                if committed and event["release_id"] != selected or not committed and event["release_id"] in graphs:
                    raise ValueError("aborted transaction became visible")
                if type(event["pid"]) is not int or event["pid"] <= 0 or event["pid"] in pids or type(event["generation"]) is not int or event["generation"] <= 0:
                    raise ValueError("missing process/generation attribution")
                if type(event["regressed_rounds"]) is not int or event["regressed_rounds"] != (2 if point.startswith("rollback_") else 0):
                    raise ValueError("missing real regression rounds")
                observed, killed, restarted = (timestamp(event[field]) for field in ("observed_at", "killed_at", "restarted_at"))
                barrier_deadline = timestamp(event["barrier_deadline"])
                if not observed <= killed <= restarted or not killed < barrier_deadline or (barrier_deadline-observed).total_seconds() > 45 or previous_restart is not None and observed < previous_restart:
                    raise ValueError("crash missed the bounded barrier")
                deadline = timestamp(event["deadline"])
                if observed >= deadline:
                    raise ValueError("publication exceeded the real health deadline")
                pids.add(event["pid"])
                releases.add(event["release_id"])
                generations.append(event["generation"])
                deadlines.append(deadline)
                previous_restart = restarted
            if not newer and (generations[1] != generations[0]+1 or generations[2] <= generations[1] or generations[3] != generations[2] or deadlines[1] <= deadlines[0] or len(set(deadlines[1:])) != 1):
                raise ValueError("publication reset or lost a durable checkpoint")
        except (KeyError, TypeError, ValueError, OverflowError):
            errors.append(f"publication {case}: missing consistent process, commit and recovery evidence")
    recovered, newer = record.get("recovered"), record.get("newer_intent")
    if isinstance(recovered, dict) and isinstance(newer, dict) and recovered.get("config_hash") == newer.get("config_hash"):
        errors.append("publication: operator configuration did not change")
    return errors


def validate_controls(record):
    errors, ids, requests = [], set(), set()
    require_proofs(record, ("cleanup",), errors, "controls")
    for case in ("early", "restored", "recovered"):
        item = record.get(case)
        if not isinstance(item, dict):
            errors.append(f"controls: missing {case}")
            continue
        for field, seen in (("promotion_id", ids), ("request_id", requests)):
            value = item.get(field)
            valid = valid_uuid(value) if field == "promotion_id" else valid_request(value)
            if not valid or (isinstance(value, str) and value in seen):
                errors.append(f"{case}: invalid/reused {field}")
            if isinstance(value, str):
                seen.add(value)
        require_proofs(item, ("restart",), errors, case)
        if case == "early":
            require_proofs(item, ("late_probe_fenced", "candidate_retired", "graph_config_preserved"), errors, case)
            if item.get("status") != "aborted": errors.append("early abort was not terminal")
            continue
        require_proofs(item, ("retained_recovered", "graph_config_restored", "candidate_pin_preserved", "artifact_reused", "retention_preserved", "candidate_regressed"), errors, case)
        if item.get("status") != "restored" or item.get("blocked") is not (case == "recovered"):
            errors.append(f"{case}: wrong restoration outcome")
        healthy_windows(item, errors, case, item.get("requested_at"))
        try:
            requested, deadline = timestamp(item["requested_at"]), timestamp(item["deadline"])
            if (deadline-requested).total_seconds() != 600:
                raise ValueError("control deadline changed")
        except (KeyError, TypeError, ValueError, OverflowError):
            errors.append(f"{case}: invalid bounded control deadline")
        if case == "recovered":
            require_proofs(item, ("blocked_receipt_replayed",), errors, case)
            blocked_id = item.get("blocked_request_id")
            if not valid_request(blocked_id) or blocked_id in requests:
                errors.append("recovery reused the blocked request")
            if type(item.get("baseline_retries")) is not int or item["baseline_retries"] < 5:
                errors.append("recovery lacks real baseline deliveries")
            try:
                start, deadline, resolved = (timestamp(item[key]) for key in ("blocked_requested_at", "blocked_deadline", "blocked_at"))
                if (deadline-start).total_seconds() != 600 or resolved < deadline or timestamp(item["requested_at"]) < resolved:
                    raise ValueError("real timeout not exercised")
            except (KeyError, TypeError, ValueError, OverflowError):
                errors.append("recovery lacks the full real restoration timeout")
    return errors


def validate_concurrent(record):
    errors = []
    require_proofs(record, ("slow_probe", "unrelated_progress_during_delay", "all_progressed_while_active", "owner_restarted", "lease_reclaimed", "late_observation_fenced", "ordinary_progress_after_control", "control_restored", "publication_unique", "artifact_reused", "cleanup"), errors, "load")
    for field, maximum in (("fleet_peak", 4), ("account_peak", 2)):
        value = record.get(field)
        if type(value) is not int or value < 2 or value > maximum:
            errors.append(f"load: invalid {field}")
    if type(record.get("fleet_limit")) is not int or record["fleet_limit"] != 4 or type(record.get("account_limit")) is not int or record["account_limit"] != 2:
        errors.append("load: changed scheduler limits")
    try:
        if timestamp(record["reclaim_at"]) < timestamp(record["previous_lease_until"]):
            raise ValueError("lease stolen early")
        control_at = timestamp(record["control_requested_at"])
    except (KeyError, TypeError, ValueError, OverflowError):
        errors.append("load: missing real lease/control timestamps")
        control_at = None
    jobs = record.get("jobs")
    if not isinstance(jobs, list) or len(jobs) <= 20:
        return errors + ["load: more than 20 accepted promotions required"]
    ids, accounts, projects, destinations = set(), set(), set(), set()
    restored, ordinary = 0, False
    for job in jobs:
        if not isinstance(job, dict):
            errors.append("load: invalid job")
            continue
        for field in ("promotion_id", "account_id", "project_id", "selected_graph"):
            if not valid_uuid(job.get(field)):
                errors.append(f"load: invalid {field}")
        identity = job.get("promotion_id")
        if isinstance(identity, str):
            if identity in ids: errors.append("load: duplicate promotion")
            ids.add(identity)
        for value, collection in ((job.get("account_id"), accounts), (job.get("project_id"), projects)):
            if isinstance(value, str): collection.add(value)
        environment = job.get("environment")
        if not isinstance(environment, str) or not environment:
            errors.append("load: missing destination environment")
        else:
            destination = (job.get("project_id"), environment)
            if all(isinstance(value,str) for value in destination):
                if destination in destinations: errors.append("load: duplicate destination")
                destinations.add(destination)
        healthy_windows(job, errors, "load job")
        if job.get("phase") == "failed": restored += 1
        elif job.get("phase") != "succeeded": errors.append("load: job did not finish")
        elif control_at is not None:
            try:
                ordinary |= timestamp(job["healthy_windows"][-1]) > control_at
            except (KeyError, TypeError, ValueError, IndexError): pass
    if len(accounts)<3 or len(projects)<3 or restored!=1 or not ordinary:
        errors.append("load: missing account isolation, restoration or ordinary progress")
    return errors


def validate_admission(record):
    errors = []
    require_proofs(record, ("waiting_restart", "concurrent_admission", "no_waiting_candidates", "account_rejected", "fleet_rejected",
                           "rejections_unaccepted", "fifo", "cancelled_without_start", "drift_conflicted",
                           "drift_preserved", "artifact_reused", "publication_unique", "cleanup"), errors, "admission")
    limits = {"fleet_active": 24, "account_active": 8, "fleet_queued": 128, "account_queued": 32,
              "wait_seconds": 900, "health_seconds": 600}
    peaks = {"ordinary_active": 24, "ordinary_account": 8, "total_active": 25, "total_account": 9,
             "queued": 128, "queued_account": 32}
    for label, expected in (("limits", limits), ("peaks", peaks)):
        actual = record.get(label)
        if not isinstance(actual, dict) or set(actual) != set(expected) or any(
            type(actual.get(key)) is not int or actual[key] != value for key, value in expected.items()
        ):
            errors.append(f"admission: wrong {label}")
    waiting = record.get("waiting")
    ids, accounts, times = set(), {}, {}
    try:
        restart = timestamp(record["waiting_restart_at"])
        if not isinstance(waiting, list) or len(waiting) != 128:
            raise ValueError("128 waiting operations required")
        for item in waiting:
            if not isinstance(item, dict) or not valid_uuid(item.get("promotion_id")) or not valid_uuid(item.get("account_id")):
                raise ValueError("missing waiting identity")
            identity, account = item["promotion_id"], item["account_id"]
            if identity in ids:
                raise ValueError("duplicate waiter")
            ids.add(identity)
            accounts[account] = accounts.get(account, 0) + 1
            queued, deadline = timestamp(item["queued_at"]), timestamp(item["wait_deadline"])
            if (deadline-queued).total_seconds() != 900 or not queued <= restart < deadline or accounts[account] > 32:
                raise ValueError("invalid original wait interval")
            times[identity] = queued, deadline
        if len(accounts) < 5 or max(accounts.values()) != 32:
            raise ValueError("fleet rejection must be independent of account rejection")
    except (KeyError, TypeError, ValueError, OverflowError):
        errors.append("admission: missing bounded, unique waiting inventory across restart")
    if any(not valid_uuid(record.get(key)) or record[key] not in ids for key in ("cancelled_id","drift_id")) or record.get("cancelled_id") == record.get("drift_id"):
        errors.append("admission: cancellation/drift are not distinct accepted waiters")
    dispatch = record.get("dispatch")
    if not isinstance(dispatch, dict):
        errors.append("admission: missing dispatch")
    else:
        require_proofs(dispatch, ("restart_before_preparation", "idempotent_replay", "deadlines_preserved"), errors, "dispatch")
        if type(dispatch.get("candidate_count")) is not int or dispatch["candidate_count"] != 1:
            errors.append("dispatch: candidate was duplicated")
        try:
            identity = dispatch["promotion_id"]
            queued, deadline = timestamp(dispatch["queued_at"]), timestamp(dispatch["wait_deadline"])
            started, health = timestamp(dispatch["started_at"]), timestamp(dispatch["health_deadline"])
            if identity not in ids or identity in (record.get("cancelled_id"),record.get("drift_id")) or times[identity] != (queued,deadline) or not restart <= started < deadline or (health-started).total_seconds() != 600:
                raise ValueError("dispatch reset time or skipped waiting")
            healthy_windows(dispatch, errors, "dispatch", dispatch["started_at"])
            if any(timestamp(value) > health for value in dispatch["healthy_windows"]):
                raise ValueError("late health evidence")
        except (KeyError, TypeError, ValueError, OverflowError):
            errors.append("dispatch: missing original queue times and fresh full health window")
    recovery = record.get("recovery")
    if not isinstance(recovery, dict):
        errors.append("admission: missing saturated recovery")
    else:
        require_proofs(recovery, ("restart", "waiting_preserved", "ordinary_progress", "retention_preserved", "baseline_only"), errors, "admission recovery")
        if not valid_request(recovery.get("request_id")) or not valid_request(recovery.get("blocked_request_id")) or recovery.get("request_id") == recovery.get("blocked_request_id"):
            errors.append("admission recovery: missing distinct operator requests")
        for key in ("promotion_id", "selected_before", "selected_after", "baseline_deployment"):
            if not valid_uuid(recovery.get(key)):
                errors.append(f"admission recovery: missing {key}")
        for key, value in (("active_before",24),("active_after",25),("account_after",9)):
            if type(recovery.get(key)) is not int or recovery[key] != value:
                errors.append(f"admission recovery: wrong {key}")
        if recovery.get("status") != "restored" or recovery.get("selected_before") == recovery.get("selected_after"):
            errors.append("admission recovery: baseline was not restored")
        try:
            requested, deadline, blocked = (timestamp(recovery[key]) for key in ("blocked_requested_at","blocked_deadline","blocked_at"))
            resumed, end = timestamp(recovery["requested_at"]), timestamp(recovery["deadline"])
            if (deadline-requested).total_seconds() != 600 or blocked < deadline or resumed < blocked or (end-resumed).total_seconds() != 600:
                raise ValueError("control windows changed")
            healthy_windows(recovery, errors, "admission recovery", recovery["requested_at"])
            if any(timestamp(value) > end for value in recovery["healthy_windows"]):
                raise ValueError("late recovery evidence")
        except (KeyError, TypeError, ValueError, OverflowError):
            errors.append("admission recovery: missing full blocked and fresh recovery windows")
    expiry = record.get("expiry")
    if not isinstance(expiry, dict):
        errors.append("admission: missing queue expiry")
    else:
        require_proofs(expiry, ("restart", "no_start", "baseline_preserved"), errors, "admission expiry")
        if not valid_uuid(expiry.get("promotion_id")) or expiry.get("promotion_id") in ids or expiry.get("reason") != "queue_timeout":
            errors.append("admission expiry: wrong identity/outcome")
        try:
            start, deadline, observed = (timestamp(expiry[key]) for key in ("queued_at","wait_deadline","observed_at"))
            if (deadline-start).total_seconds() != 900 or observed < deadline:
                raise ValueError("queue time shortened")
        except (KeyError, TypeError, ValueError, OverflowError):
            errors.append("admission expiry: real 15-minute wait was not exercised")
    return errors


def validate_migration(record, case):
    errors = []
    require_proofs(record, ("lost_response", "queued_restart", "admission_disabled", "destination_only", "artifact_reused",
                            "terminal_restart", "schema_preserved", "cleanup"), errors, case)
    for field in ("promotion_id", "task_id", "run_id", "baseline_graph", "selected_graph"):
        if not valid_uuid(record.get(field)):
            errors.append(f"{case}: invalid {field}")
    if len({record.get(key) for key in ("promotion_id", "task_id", "run_id") if isinstance(record.get(key), str)}) != 3:
        errors.append(f"{case}: reused operation/task/SQL identity")
    for field in ("task_count", "attempts", "executions", "source_executions"):
        if type(record.get(field)) is not int or record[field] != 1:
            errors.append(f"{case}: duplicate/missing {field}")
    recovery, failure, rollback = case == "migration_recovery", case == "migration_failure", case == "migration_rollback"
    want_exit = None if recovery else 23 if failure else 0
    if "exit_code" not in record or record["exit_code"] != want_exit or (not recovery and type(record["exit_code"]) is not int):
        errors.append(f"{case}: wrong or fabricated command exit")
    if record.get("migration_status") != ("uncertain" if recovery else "failed" if failure else "succeeded"):
        errors.append(f"{case}: original migration receipt was lost")
    if record.get("phase") != ("failed" if failure or rollback else "succeeded"):
        errors.append(f"{case}: wrong final phase")
    try:
        admitted, deadline, started, finished = (timestamp(record[key]) for key in
                                                ("migration_admitted_at", "migration_deadline", "task_started_at", "task_finished_at"))
        if (deadline-admitted).total_seconds() != 660 or not admitted <= started < admitted + timedelta(seconds=60) or finished < started:
            raise ValueError("migration admission/start window changed")
    except (KeyError, TypeError, ValueError, OverflowError):
        errors.append(f"{case}: missing original migration/start/exit timing")
    if failure:
        require_proofs(record, ("graph_config_preserved",), errors, case)
        if record.get("selected_graph") != record.get("baseline_graph"):
            errors.append("failed command published a graph")
        return errors
    require_proofs(record, ("running_restart", "completed_restart", "read_only_health"), errors, case)
    if not valid_uuid(record.get("cutover_graph")) or record.get("cutover_graph") == record.get("baseline_graph"):
        errors.append(f"{case}: missing new cutover graph")
    if record.get("graph_config_restored") is not rollback:
        errors.append(f"{case}: wrong rollback/config result")
    if rollback:
        if type(record.get("regressed_rounds")) is not int or record["regressed_rounds"] < 2 or record.get("selected_graph") in (record.get("baseline_graph"), record.get("cutover_graph")):
            errors.append("migration rollback lacks regression and new restoration graph")
    else:
        if record.get("selected_graph") != record.get("cutover_graph"):
            errors.append(f"{case}: selected graph is not the qualified candidate")
        healthy_windows(record, errors, case, record.get("health_started_at"))
    if type(record.get("preflight_rounds")) is not int or record["preflight_rounds"] < 2:
        errors.append(f"{case}: missing baseline/candidate preflight")
    try:
        health, end = timestamp(record["health_started_at"]), timestamp(record["health_deadline"])
        if (end-health).total_seconds() != 600 or health < timestamp(record["task_finished_at"]):
            raise ValueError("not a fresh full health window after task completion")
    except (KeyError, TypeError, ValueError, OverflowError):
        errors.append(f"{case}: missing fresh full preflight deadline")
    if recovery:
        item = record.get("recovery")
        if not isinstance(item, dict):
            return errors + ["missing migration recovery evidence"]
        require_proofs(item, ("quiescing_restart", "competing_rejected", "session_gone", "original_receipt_preserved"), errors, case)
        for field in ("node_id", "runtime_id", "lease_token"):
            if not valid_uuid(item.get(field)):
                errors.append(f"recovery: invalid {field}")
        if not valid_request(item.get("request_id")) or not isinstance(item.get("evidence_hash"), str) or not re.fullmatch(r"[a-f0-9]{64}", item["evidence_hash"]) or item.get("status") != "resumed":
            errors.append("recovery: missing bound operator decision")
        try:
            lease, uncertain, requested, destroyed, resolved, deadline = (timestamp(item[key]) for key in
                ("previous_lease_until", "uncertain_at", "requested_at", "destroyed_at", "resolved_at", "deadline"))
            if not lease <= uncertain <= requested <= destroyed <= resolved <= deadline or (deadline-requested).total_seconds() != 600 or timestamp(record["health_started_at"]) < destroyed:
                raise ValueError("recovery skipped lease expiry, teardown or fresh health")
        except (KeyError, TypeError, ValueError, OverflowError):
            errors.append("recovery: missing ordered lease/teardown/operator evidence")
    return errors


def validate_observation(name, record):
    errors = []
    if not isinstance(record, dict):
        return [f"{name}: observation must be an object"]
    if type(record.get("schema_version")) is not int or record["schema_version"] != 1 or record.get("scenario") != SCENARIOS[name]:
        errors.append(f"{name}: wrong observation schema/scenario")
    if SCENARIOS[name].startswith("migration_"):
        return errors + validate_migration(record, SCENARIOS[name])
    if SCENARIOS[name] == "publication_crashes":
        return errors + validate_publication(record)
    if SCENARIOS[name] == "admission":
        return errors + validate_admission(record)
    if SCENARIOS[name] == "operator_controls":
        return errors + validate_controls(record)
    if SCENARIOS[name] == "concurrent":
        return errors + validate_concurrent(record)
    for proof in PROOFS:
        if record.get(proof) is not True:
            errors.append(f"{name}: missing {proof}")
    for identity in ("promotion_id", "retry_release_id", "retry_deployment_id", "candidate_release_id"):
        if not valid_uuid(record.get(identity)):
            errors.append(f"{name}: invalid {identity}")
    if record.get("retry_release_id") == record.get("candidate_release_id"):
        errors.append(f"{name}: retry was retargeted to the candidate")
    for field, minimum in (("preflight_rounds", 2), ("candidate_jobs", 5)):
        value = record.get(field)
        if type(value) is not int or value < minimum:
            errors.append(f"{name}: insufficient {field}")
    if type(record.get("retry_attempts")) is not int or record["retry_attempts"] != 2:
        errors.append(f"{name}: missing actual retry")
    rollback = SCENARIOS[name] == "rolled_back"
    if record.get("graph_config_restored") is not rollback:
        errors.append(f"{name}: wrong graph/config restoration result")
    if rollback:
        rounds = record.get("regressed_rounds")
        if type(rounds) is not int or rounds < 2:
            errors.append(f"{name}: missing repeated regression evidence")
    else:
        try:
            raw = record["healthy_windows"]
            if not isinstance(raw, list) or len(raw) < 2:
                raise ValueError("two windows required")
            windows = [datetime.fromisoformat(value.replace("Z", "+00:00")) for value in raw]
            if any(value.tzinfo is None for value in windows) or any(
                (end - start).total_seconds() < 60 for start, end in zip(windows, windows[1:])
            ):
                raise ValueError("windows must be fresh and minute-spaced")
        except (KeyError, TypeError, ValueError, AttributeError):
            errors.append(f"{name}: missing disjoint real-time healthy windows")
    return errors


def qualify(log, sha, group="all"):
    errors, passed, records = [], set(), {}
    if not re.fullmatch(r"[a-f0-9]{40}", sha):
        errors.append("invalid source SHA")
    groups = list(GROUPS) if group == "all" else [group]
    required = REQUIRED_TESTS if group == "all" else GROUPS[group]
    scenarios = set(SCENARIOS) & required
    current = None
    for line in log.splitlines():
        start = re.fullmatch(r"=== RUN   (\w+)", line)
        if start:
            if current is not None:
                errors.append("overlapping or unfinished native scenarios")
            current = start[1]
        if re.match(r"\s*--- (FAIL|SKIP):", line) or re.match(r"(?:FAIL\b|panic:|fatal error:)", line):
            errors.append("native test failed, skipped or aborted")
        if "environment promotion native:" in line:
            if current not in scenarios or current in records:
                errors.append("unattributed or duplicate promotion observation")
            else:
                try:
                    record = json.loads(line.split("environment promotion native:", 1)[1])
                    records[current] = record
                    errors.extend(validate_observation(current, record))
                except (ValueError, TypeError):
                    errors.append(f"{current}: malformed observation")
        result = re.match(r"^--- (PASS|FAIL|SKIP): (\w+)(?: |$)", line)
        if result:
            if result[2] in required and result[1] == "PASS":
                if current != result[2] or result[2] in passed:
                    errors.append("duplicate or unattributed scenario result")
                passed.add(result[2])
            current = None
    for name in required:
        if name not in passed:
            errors.append(f"missing PASS: {name}")
    for name in scenarios:
        if name not in records:
            errors.append(f"missing observation: {name}")
    migration_records = [record for name, record in records.items()
                         if SCENARIOS[name].startswith("migration_") and isinstance(record, dict)]
    for field in ("promotion_id", "task_id", "run_id"):
        identities = [record.get(field) for record in migration_records if isinstance(record.get(field), str)]
        if len(set(identities)) != len(identities):
            errors.append(f"migration scenarios reused {field}")
    if current is not None:
        errors.append("unfinished native scenario")
    lines = log.splitlines()
    if [line for line in lines if line.startswith("native promotion qualification: final_leakcheck_rc=")] != [f"native promotion qualification: final_leakcheck_rc=0 group={item}" for item in groups]:
        errors.append("missing clean final leakcheck")
    if [line for line in lines if line.startswith("native promotion qualification: sha=")] != [f"native promotion qualification: sha={sha} final_rc=0 group={item}" for item in groups]:
        errors.append("missing successful source-bound runner completion")
    return {"schema_version": 1, "source_sha": sha,
            "log_sha256": hashlib.sha256(log.encode()).hexdigest(),
            "scope": "project-environment-promotion-only" + ("" if group == "all" else "/" + group), "qualified": not errors,
            "admission_enabled": False, "tests": sorted(passed & required),
            "scenarios": records, "errors": errors}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--log", required=True, type=Path)
    parser.add_argument("--sha", required=True)
    parser.add_argument("--group", choices=("all", *GROUPS), default="all")
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    try:
        report = qualify(args.log.read_bytes().decode("utf-8"), args.sha, args.group)
    except (OSError, UnicodeError) as exc:
        report = {"schema_version": 1, "source_sha": args.sha, "qualified": False, "errors": [str(exc)]}
    args.output.write_text(json.dumps(report, indent=2) + "\n")
    return 0 if report["qualified"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
