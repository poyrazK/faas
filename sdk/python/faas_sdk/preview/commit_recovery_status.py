"""Read-only reconciliation of current delivery evidence and saved recovery attempts."""

from __future__ import annotations

import asyncio
import copy
import json
import math
import re
from collections.abc import Callable
from datetime import UTC, datetime
from email.utils import parsedate_to_datetime
from typing import Any
from uuid import UUID, uuid4

import httpx

from .api.events import get_event_recipient_blockers
from .client import AuthenticatedClient
from .commit_delivery_report import CommitDeliveryReadError, CommitDeliveryReportError, aget_commit_delivery_report
from .commit_recovery_plan import _same_acceptance
from .models.event_recipient_blocker_batch_request import EventRecipientBlockerBatchRequest
from .models.event_recipient_blocker_batch_response import EventRecipientBlockerBatchResponse


class _CommitRecoveryReadErrors(Exception):
    def __init__(self, errors: list[Exception]):
        self.errors = errors
        super().__init__("recovery status reads failed")


class CommitRecoveryStatusError(Exception):
    def __init__(self, result: dict[str, Any]):
        self.result = result
        super().__init__("Commit recovery status read failed; partial observations are attached")


def _observe(row: dict[str, Any], evidence: dict[str, Any] | None) -> None:
    if evidence is None:
        return
    if evidence.get("observed_at"):
        row["observed_at"] = evidence["observed_at"]
    row["reason"] = evidence["evidence_status"]
    recipient = next(
        (
            item.get("recipient")
            for item in evidence["subscribers"]
            if item["subscription_id"].lower() == row["attempt"]["subscription_id"].lower()
        ),
        None,
    )
    if not recipient:
        if evidence["evidence_status"] == "available":
            row["reason"] = "recipient_unavailable"
        return
    row["recipient"] = recipient
    generation = recipient.get("routing", {}).get("generation")
    if type(generation) is int:
        row["generation"] = generation
    expected = row.get("expected_generation")
    if expected is not None:
        row["generation_relation"] = (
            "unavailable"
            if type(generation) is not int
            else "advanced"
            if generation > expected
            else "regressed"
            if generation < expected
            else "unchanged"
        )
    if recipient.get("workflow_name"):
        row.update(status="unsupported_recipient", reason="unsupported_recipient")
        return
    execution, matched = _selected_execution(row)
    if matched:
        row["attribution"] = "matches_acknowledged_execution"
    if execution:
        if execution.get("invocation_id"):
            row["execution_id"] = execution["invocation_id"]
        if execution.get("state"):
            row["execution_state"] = execution["state"]
    state = execution.get("state") if execution else recipient.get("routing", {}).get("state")
    if not execution and recipient.get("cancellation"):
        state = "cancelled"
    if not execution and recipient.get("execution_unavailable") and state != "cancelled":
        row.update(status="unresolved", reason=recipient["execution_unavailable"])
        return
    row["reason"] = state or "evidence_unavailable"
    row["status"] = (
        "completed"
        if state == "completed"
        else "active"
        if state in {"pending", "processing", "dispatching"}
        else "failed"
        if state in {"failed", "dead_letter", "cancelled", "expired", "superseded"}
        else "unresolved"
    )


async def aget_commit_recovery_status(
    client: AuthenticatedClient,
    source: UUID | str,
    reviewed: dict[str, Any],
    applied: dict[str, Any],
    *,
    limit: int = 50,
    recipient_pages: int = 1,
    timeout: float = 120,
    max_observation_age: float = 0,
    diagnostics: bool = False,
    max_diagnostic_age: float = 0,
    resume: dict[str, Any] | None = None,
    refresh: dict[str, Any] | None = None,
) -> dict[str, Any]:
    """Read guarded evidence only; never resolve an unknown write from delivery advancement."""
    if resume is not None and refresh is not None:
        raise ValueError("choose resume or refresh")
    _validate_observation_age(max_observation_age)
    _validate_observation_age(max_diagnostic_age)
    if max_diagnostic_age > 0 and not diagnostics:
        raise ValueError("maximum diagnostic age requires diagnostics")
    source = str(UUID(str(source)))
    plan, result = copy.deepcopy(reviewed), copy.deepcopy(applied)
    previous = copy.deepcopy(resume if resume is not None else refresh)
    diagnostic_previous = copy.deepcopy(previous) if resume is not None else None
    out: dict[str, Any] = {
        "source_id": source,
        "subscription_id": plan["report"].get("subscription_id", ""),
        "items": [],
        "counts": {},
        "remaining": 0,
        "complete": False,
    }
    if (
        type(limit) is not int
        or not 1 <= limit <= 50
        or type(recipient_pages) is not int
        or not 1 <= recipient_pages <= 20
        or not 0 < timeout <= 86400
        or plan["report"]["source_id"] != source
        or result["source_id"].lower() != source
        or len(plan["report"]["items"]) > 200
        or len(plan["items"]) > 200 * 200 * 20
        or len(result["items"]) > 200 * 200 * 20
    ):
        raise ValueError("invalid recovery status budgets or source")
    publications = {item["acceptance"]["event_id"].lower(): item for item in plan["report"]["items"]}
    selection: dict[str, Any] = {}
    for item in plan["items"]:
        sub = str(UUID(item["subscription_id"]))
        key = item["event_id"].lower() + ":" + sub
        if (
            item["event_id"].lower() not in publications
            or int(UUID(sub)) == 0
            or key in selection
            or (plan["report"].get("subscription_id") and plan["report"]["subscription_id"].lower() != sub)
            or item["status"] not in {"eligible", "skipped"}
            or (item["status"] == "eligible" and not item.get("action"))
        ):
            raise ValueError("invalid recovery status selection")
        selection[key] = item
    seen: set[str] = set()
    for attempt in result["items"]:
        key = attempt["event_id"].lower() + ":" + str(UUID(attempt["subscription_id"]))
        selected = selection.get(key)
        if (
            selected is None
            or key in seen
            or attempt["status"] not in {"queued", "unknown", "rejected", "read_error", "skipped", "not_attempted"}
            or (
                attempt["status"] in {"queued", "unknown", "rejected", "read_error"}
                and selected["status"] != "eligible"
            )
        ):
            raise ValueError("apply result does not match reviewed selection")
        seen.add(key)
        publication = publications[attempt["event_id"].lower()]["acceptance"].get("publication")
        if not publication:
            raise ValueError("saved publication identity mismatch")
        row = {
            "selection": {"publication": publication, "reviewed": selected},
            "attempt": attempt,
            "status": "not_observed",
            "reason": "status_budget",
            "attribution": "unproven",
        }
        action = selected.get("action") or {}
        generation = (
            (action.get("body") or {}).get("expected_generation") if action.get("kind") == "routing_replay" else None
        )
        if type(generation) is int and generation >= 0:
            row["expected_generation"] = generation
        out["items"].append(row)
    if previous is not None:
        if "refreshing" in previous and not isinstance(previous["refreshing"], bool):
            raise ValueError("invalid saved refresh flag")
        if any(not row.get("selection") for row in previous["items"]):
            raise ValueError("saved status lacks selection snapshots; run recovery-status without --resume")
        if (
            previous.get("source_id", "").lower() != source
            or (previous.get("subscription_id") or "").lower() != (out.get("subscription_id") or "").lower()
            or len(previous["items"]) != len(out["items"])
        ):
            raise ValueError("saved status selection mismatch")
        for row, expected in zip(previous["items"], out["items"], strict=True):
            if (
                not row.get("selection")
                or not _same_selection(row["selection"], expected["selection"])
                or not _same_json(row["attempt"], expected["attempt"])
                or not _same_json(row.get("expected_generation"), expected.get("expected_generation"))
            ):
                raise ValueError("saved status does not match reviewed plan and apply result")
            if (
                row["status"]
                not in {
                    "completed",
                    "active",
                    "failed",
                    "unresolved",
                    "unsupported_recipient",
                    "read_error",
                    "not_observed",
                }
                or row["attribution"] not in {"unproven", "matches_acknowledged_execution"}
                or (row["attribution"] == "matches_acknowledged_execution" and row["attempt"]["status"] != "queued")
            ):
                raise ValueError("invalid saved observation or attribution")
            observed_at = row.get("observed_at")
            if observed_at is not None and (
                not isinstance(observed_at, str) or not _same_acceptance(observed_at, observed_at)
            ):
                raise ValueError("invalid saved observation timestamp")
            if bool(previous.get("refreshing", False)) != (row.get("previous_observation") is not None):
                raise ValueError("invalid saved refresh baseline")
            if row.get("previous_observation") is not None:
                _validate_observation(row["previous_observation"], row["attempt"]["status"])
        for row in previous["items"]:
            row.pop("diagnostics", None)
        out["items"] = previous["items"]
        out["refreshing"] = previous.get("refreshing", False)
    if refresh is not None:
        out["refreshing"] = True
        rows = []
        for old in out["items"]:
            row = {
                "attempt": old["attempt"],
                "selection": old["selection"],
                "previous_observation": _observation(old),
                "status": "not_observed",
                "reason": "status_budget",
                "attribution": "unproven",
            }
            if "expected_generation" in old:
                row["expected_generation"] = old["expected_generation"]
            rows.append(row)
        out["items"] = rows
    _recount(out)
    pending = [
        index
        for status in ("not_observed", "read_error")
        for index, row in enumerate(out["items"])
        if row["status"] == status
    ]
    if max_observation_age > 0:
        now = datetime.now(UTC).timestamp()
        stale = [
            index
            for index, row in enumerate(out["items"])
            if row["status"] not in {"not_observed", "read_error"}
            and _freshness_kind(row, now, max_observation_age) != "fresh"
        ]

        def priority(index: int) -> float:
            value = _observation_timestamp(out["items"][index])
            return value if value is not None and value <= now else -math.inf

        pending.extend(sorted(stale, key=priority))
    cause: BaseException | None = None
    reading_index: int | None = None
    try:
        async with asyncio.timeout(timeout):
            await aget_commit_delivery_report(
                client, source, refresh=plan["report"], recipient_pages=1, timeout=timeout
            )
            for index in pending[:limit]:
                reading_index = index
                old = out["items"][index]
                row = {
                    "attempt": old["attempt"],
                    "selection": old["selection"],
                    "status": "unresolved",
                    "reason": "evidence_unavailable",
                    "attribution": "unproven",
                }
                if "expected_generation" in old:
                    row["expected_generation"] = old["expected_generation"]
                if "previous_observation" in old:
                    row["previous_observation"] = old["previous_observation"]
                out["items"][index] = row
                original = copy.deepcopy(publications[row["attempt"]["event_id"].lower()])
                original["subscribers"] = []
                saved = {
                    **plan["report"],
                    "subscription_id": row["attempt"]["subscription_id"].lower(),
                    "items": [original],
                }
                try:
                    report = await aget_commit_delivery_report(
                        client, source, refresh=saved, recipient_pages=recipient_pages, timeout=timeout
                    )
                    _observe(row, report["items"][0] if report["items"] else None)
                except Exception as error:
                    if isinstance(error, CommitDeliveryReportError):
                        _observe(row, error.report["items"][0] if error.report["items"] else None)
                    row.update(status="read_error", reason="evidence_read_failed")
                    out["complete"] = False
                    cause = _CommitRecoveryReadErrors([cause, error]) if isinstance(cause, Exception) else error
                reading_index = None
            if diagnostics:
                out = (
                    await adiagnose_commit_recovery_status(
                        client, out, previous=diagnostic_previous, max_diagnostic_age=max_diagnostic_age
                    )
                    if cause is None
                    else _prepare_diagnostics(out, diagnostic_previous, max_diagnostic_age)
                )
    except (Exception, asyncio.CancelledError) as error:
        if isinstance(error, CommitRecoveryStatusError):
            out = error.result
        out["complete"] = False
        cause = error
        for row in out["items"]:
            if row["status"] == "not_observed":
                row["reason"] = (
                    "interrupted"
                    if isinstance(error, (TimeoutError, asyncio.CancelledError))
                    else "validation_read_failed"
                )
        if reading_index is not None:
            out["items"][reading_index].update(status="read_error", reason="evidence_read_failed")
    if cause is not None and diagnostics and "diagnostics_progress" not in out:
        out = _prepare_diagnostics(out, diagnostic_previous, max_diagnostic_age)
    _recount(out)
    if cause is not None:
        out["complete"] = False
    if cause is not None:
        raise CommitRecoveryStatusError(out) from cause
    return out


def _same_json(left: Any, right: Any) -> bool:
    return json.dumps(left, sort_keys=True, separators=(",", ":"), allow_nan=False) == json.dumps(
        right, sort_keys=True, separators=(",", ":"), allow_nan=False
    )


def _same_selection(left: dict[str, Any], right: dict[str, Any]) -> bool:
    publication = left.get("publication")
    if (
        not isinstance(publication, dict)
        or not isinstance(publication.get("accepted_at"), str)
        or not _same_acceptance(publication["accepted_at"], right["publication"]["accepted_at"])
    ):
        return False
    normalised = {**left, "publication": {**publication, "accepted_at": right["publication"]["accepted_at"]}}
    return _same_json(normalised, right)


def _recount(out: dict[str, Any]) -> None:
    out["counts"], out["remaining"] = {}, 0
    for row in out["items"]:
        changes = _compare_observation(row.get("previous_observation"), _observation(row))
        if changes:
            row["changes"] = changes
        else:
            row.pop("changes", None)
        status = row["status"]
        out["counts"][status] = out["counts"].get(status, 0) + 1
        if status in {"not_observed", "read_error"}:
            out["remaining"] += 1
    out["complete"] = out["remaining"] == 0


def _selected_execution(row: dict[str, Any]) -> tuple[dict[str, Any] | None, bool]:
    recipient = row.get("recipient") or {}
    if recipient.get("workflow_name"):
        return None, False
    original = recipient.get("execution")
    latest = (recipient.get("recovery") or {}).get("latest_replay")
    execution = latest or original
    matched = False
    ack = row["attempt"].get("result")
    if row["attempt"]["status"] == "queued" and isinstance(ack, dict) and isinstance(ack.get("id"), str):
        try:
            acknowledged = str(UUID(ack["id"]))
        except ValueError:
            acknowledged = None
        if acknowledged:
            for candidate in (original, latest):
                if candidate and candidate.get("invocation_id", "").lower() == acknowledged:
                    execution, matched = candidate, True
    return execution, matched


def _observation(row: dict[str, Any]) -> dict[str, Any]:
    execution, _ = _selected_execution(row)
    observation = {"status": row["status"], "attribution": row["attribution"]}
    for field in ("reason", "observed_at", "generation", "generation_relation"):
        if row.get(field) is not None and row[field] != "":
            observation[field] = row[field]
    for field, key in (("execution_id", "invocation_id"), ("execution_state", "state")):
        value = row.get(field) or (execution or {}).get(key)
        if value:
            observation[field] = value
    return observation


def _validate_observation(previous: dict[str, Any], attempt: str) -> None:
    if (
        previous["status"]
        not in {"completed", "active", "failed", "unresolved", "unsupported_recipient", "read_error", "not_observed"}
        or previous["attribution"] not in {"unproven", "matches_acknowledged_execution"}
        or (previous["attribution"] == "matches_acknowledged_execution" and attempt != "queued")
    ):
        raise ValueError("invalid saved refresh observation")
    observed_at = previous.get("observed_at")
    if observed_at is not None and (not isinstance(observed_at, str) or not _same_acceptance(observed_at, observed_at)):
        raise ValueError("invalid saved refresh timestamp")


def _compare_observation(previous: dict[str, Any] | None, current: dict[str, Any]) -> list[dict[str, Any]]:
    if (
        previous is None
        or previous["status"] in {"not_observed", "read_error"}
        or current["status"] in {"not_observed", "read_error"}
    ):
        return []
    changes = []
    for field in (
        "status",
        "reason",
        "generation",
        "generation_relation",
        "attribution",
        "execution_id",
        "execution_state",
    ):
        fallback = None if field == "generation" else ""
        before, after = previous.get(field, fallback), current.get(field, fallback)
        if _same_json(before, after):
            continue
        change = {"field": field, "previous": before, "current": after}
        if previous.get("observed_at"):
            change["previous_observed_at"] = previous["observed_at"]
        if current.get("observed_at"):
            change["observed_at"] = current["observed_at"]
        changes.append(change)
    return changes


def commit_recovery_status_settled(out: dict[str, Any]) -> bool:
    """Require matching terminal execution evidence for every queued attempt."""
    if not out["complete"] or out["remaining"]:
        return False
    for row in out["items"]:
        if row["attempt"]["status"] == "queued":
            if (
                row["status"] not in {"completed", "failed"}
                or row["attribution"] != "matches_acknowledged_execution"
                or row.get("execution_state")
                not in {
                    "completed",
                    "failed",
                    "cancelled",
                    "expired",
                    "superseded",
                }
            ):
                return False
        elif row["attempt"]["status"] not in {"rejected", "skipped", "not_attempted"}:
            return False
    return True


async def awatch_commit_recovery_status(
    client: AuthenticatedClient,
    source: UUID | str,
    reviewed: dict[str, Any],
    applied: dict[str, Any],
    *,
    limit: int = 50,
    recipient_pages: int = 1,
    timeout: float = 120,
    interval: float = 1,
    read_retries: int = 0,
    max_observation_age: float = 0,
    diagnostics: bool = False,
    watch_diagnostics: bool = False,
    max_diagnostic_age: float = 0,
    stall_threshold: float = 0,
    resume: dict[str, Any] | None = None,
    refresh: dict[str, Any] | None = None,
    on_progress: Callable[[dict[str, Any]], None] | None = None,
    durable_alerts: bool = False,
    alert_acknowledgements: list[dict[str, Any]] | None = None,
    on_alert_acknowledgements: Callable[[dict[str, Any]], list[dict[str, Any]]] | None = None,
) -> dict[str, Any]:
    """Bounded polling; timeout/cancellation attaches the latest observations."""
    diagnostics = diagnostics or watch_diagnostics
    if (
        not math.isfinite(interval)
        or not 0.1 <= interval <= 60
        or not math.isfinite(timeout)
        or not 0 < timeout <= 86400
    ):
        raise ValueError("invalid recovery watch options")
    _validate_observation_age(max_observation_age)
    _validate_observation_age(stall_threshold)
    if stall_threshold > 0 and (not diagnostics or max_diagnostic_age <= 0):
        raise ValueError("stall threshold requires diagnostics and a positive diagnostic age limit")
    _validate_observation_age(max_diagnostic_age)
    if max_diagnostic_age > 0 and not diagnostics:
        raise ValueError("maximum diagnostic age requires diagnostics")
    if (durable_alerts and stall_threshold == 0) or (
        not durable_alerts and (alert_acknowledgements or on_alert_acknowledgements is not None)
    ):
        raise ValueError("durable alerts require stall detection; acknowledgements require durable alerts")
    if alert_acknowledgements is not None and (
        not isinstance(alert_acknowledgements, list) or len(alert_acknowledgements) > _ALERT_QUEUE_MAX
    ):
        raise ValueError("invalid alert acknowledgement bounds")
    initial_acks = copy.deepcopy([] if alert_acknowledgements is None else alert_acknowledgements)
    if type(read_retries) is not int or not 0 <= read_retries <= 20:
        raise ValueError("invalid recovery watch read retry budget")
    if resume is not None and refresh is not None:
        raise ValueError("choose resume or refresh")
    plan, result = copy.deepcopy(reviewed), copy.deepcopy(applied)
    previous = copy.deepcopy(resume if resume is not None else refresh)
    diagnostic_previous = copy.deepcopy(previous) if resume is not None else None
    refreshing = refresh is not None or (
        previous is not None and all(row["status"] not in {"not_observed", "read_error"} for row in previous["items"])
    )
    out: dict[str, Any] = {"source_id": str(source), "items": [], "counts": {}, "remaining": 0, "complete": False}
    loop = asyncio.get_running_loop()
    deadline = loop.time() + timeout
    used_retries, passes = 0, 0
    reason, phase = "read_failure", "read"
    estimator = _DiagnosticEstimator()
    stalls = _StallTracker()
    incidents = _IncidentTracker(durable_alerts)
    alert_previous = previous
    alerts_restored = False

    def restore_alerts() -> None:
        nonlocal alerts_restored
        if not alerts_restored:
            incidents.restore(alert_previous, out, initial_acks)
            alerts_restored = True

    estimate_delay_ms = interval * 1000

    async def read_diagnostics(status: dict[str, Any]) -> dict[str, Any]:
        diagnostic_started = loop.time()
        try:
            sampled = await adiagnose_commit_recovery_status(
                client,
                status,
                previous=diagnostic_previous,
                max_diagnostic_age=max_diagnostic_age,
                strict_reads=watch_diagnostics,
            )
        except CommitRecoveryStatusError as error:
            estimator.observe(error.result, (loop.time() - diagnostic_started) * 1000)
            stalls.observe(error.result, max_diagnostic_age, stall_threshold, loop.time())
            raise
        estimator.observe(sampled, (loop.time() - diagnostic_started) * 1000)
        stalls.observe(sampled, max_diagnostic_age, stall_threshold, loop.time())
        return sampled

    def summarize(stop_reason: str) -> dict[str, Any]:
        consumers = stalls.summarize(out, max_diagnostic_age, stall_threshold)
        lifecycle = incidents.summarize(out, consumers, max_diagnostic_age, stall_threshold)
        freshness = _freshness_counts(out, max_observation_age) if max_observation_age > 0 else None
        return {
            "watch_mode": "diagnostic_coverage" if watch_diagnostics else "recovery_settlement",
            "diagnostic_coverage_complete": (
                bool(
                    out.get("diagnostics_progress", {}).get("complete")
                    and out.get("diagnostics_progress", {}).get("fresh_complete")
                )
                if stop_reason == "diagnostic_coverage_complete"
                else _diagnostic_coverage_complete(out, max_diagnostic_age)
            ),
            **(
                {
                    "diagnostic_estimate": estimator.estimate(
                        out, (loop.time() - (deadline - timeout)) * 1000, estimate_delay_ms
                    )
                }
                if "diagnostics_progress" in out
                else {}
            ),
            **(
                {
                    "stall_threshold_ms": stall_threshold * 1000,
                    "consumer_stalls": consumers,
                    **lifecycle,
                }
                if stall_threshold > 0
                else {}
            ),
            **(
                {"alert_outbox": incidents.outbox(lifecycle["blocker_incidents"])}
                if durable_alerts and alerts_restored
                else {}
            ),
            "stop_reason": stop_reason,
            "passes": passes,
            "retries_consumed": used_retries,
            "elapsed_ms": max(0, int((loop.time() - (deadline - timeout)) * 1000)),
            "acknowledged_executions_terminal": commit_recovery_status_settled(out)
            and (freshness is None or freshness["fresh"] == len(out["items"])),
            **(
                {
                    "max_observation_age_ms": max_observation_age * 1000,
                    "freshness": freshness,
                }
                if max_observation_age > 0
                else {}
            ),
        }

    timeout_scope = asyncio.timeout(timeout)
    try:
        async with timeout_scope:
            while True:
                delay, failed = interval, False
                incidents.begin_pass()
                passes += 1
                try:
                    out = await aget_commit_recovery_status(
                        client,
                        source,
                        plan,
                        result,
                        limit=limit,
                        recipient_pages=recipient_pages,
                        timeout=max(0.001, deadline - loop.time()),
                        max_observation_age=max_observation_age,
                        resume=previous if not refreshing else None,
                        refresh=previous if refreshing else None,
                    )
                    restore_alerts()
                    if diagnostics:
                        out = await read_diagnostics(out)
                except CommitRecoveryStatusError as error:
                    out = error.result
                    if not _watch_validation_failure(error):
                        restore_alerts()
                    if diagnostics and "diagnostics_progress" not in out:
                        out = _prepare_diagnostics(out, diagnostic_previous, max_diagnostic_age)
                        error.result = out
                    retry_after = _read_retry(error.__cause__)
                    if retry_after is None or used_retries >= read_retries:
                        if retry_after is not None and read_retries > 0 and used_retries >= read_retries:
                            reason = "retry_exhausted"
                        raise
                    used_retries += 1
                    failed = True
                    delay = max(min(60, interval * 2 ** (used_retries - 1)), retry_after)
                estimate_delay_ms = delay * 1000
                out["monitoring"] = summarize("running")
                phase = "callback"
                if on_progress is not None:
                    on_progress(copy.deepcopy(out))
                if on_alert_acknowledgements is not None:
                    incidents.acknowledge(on_alert_acknowledgements(copy.deepcopy(out)))
                if incidents.queue_full:
                    reason, phase = "alert_queue_full", "read"
                    raise RuntimeError("alert queue full; acknowledge pending alerts and resume")
                phase = "read"
                if loop.time() >= deadline:
                    raise TimeoutError("watch deadline exceeded")
                if not failed and watch_diagnostics and _diagnostic_coverage_complete(out, max_diagnostic_age):
                    out["monitoring"] = summarize("diagnostic_coverage_complete")
                    return out
                if (
                    not failed
                    and not watch_diagnostics
                    and commit_recovery_status_settled(out)
                    and _observations_fresh(out, max_observation_age)
                ):
                    out["monitoring"] = summarize("settled")
                    return out
                diagnostic_previous = out
                previous, refreshing = (
                    out,
                    not failed and out["remaining"] == 0 and _observations_fresh(out, max_observation_age),
                )
                await asyncio.sleep(delay)
    except (Exception, asyncio.CancelledError) as error:
        if isinstance(error, CommitRecoveryStatusError):
            out = error.result
        if timeout_scope.expired() or loop.time() >= deadline:
            reason = "timeout"
        elif _watch_cancelled(error):
            reason = "cancelled"
        elif phase == "callback":
            reason = "callback_failure"
        elif _watch_validation_failure(error):
            reason = "validation_failure"
        out["monitoring"] = summarize(reason)
        if isinstance(error, CommitRecoveryStatusError):
            raise
        raise CommitRecoveryStatusError(out) from error


def _read_retry(error: BaseException | None) -> float | None:
    if isinstance(error, _CommitRecoveryReadErrors):
        delays = [_read_retry(cause) for cause in error.errors]
        return (
            None
            if any(delay is None for delay in delays)
            else max([0.0] + [float(delay) for delay in delays if delay is not None])
        )
    if isinstance(error, (CommitRecoveryStatusError, CommitDeliveryReportError)):
        return _read_retry(error.__cause__)
    status = getattr(error, "status_code", None)
    if status is not None:
        if status not in {429, 500, 502, 503, 504}:
            return None
        value = getattr(error, "retry_after", None)
        if value:
            try:
                delay = (
                    float(value)
                    if str(value).isdigit()
                    else (parsedate_to_datetime(value) - datetime.now(UTC)).total_seconds()
                )
                return max(0.0, delay) if math.isfinite(delay) else 86400.0
            except (ValueError, TypeError, OverflowError):
                pass
        return 0.0
    if isinstance(error, (httpx.NetworkError, httpx.TimeoutException, httpx.RemoteProtocolError)):
        return 0.0
    return None


def _watch_cancelled(error: BaseException | None) -> bool:
    if isinstance(error, asyncio.CancelledError):
        return True
    return error is not None and _watch_cancelled(error.__cause__)


def _watch_validation_failure(error: BaseException | None) -> bool:
    if isinstance(error, (ValueError, TypeError)):
        return True
    if isinstance(error, _CommitRecoveryReadErrors):
        return any(_watch_validation_failure(cause) for cause in error.errors)
    return error is not None and _watch_validation_failure(error.__cause__)


def _validate_observation_age(age: float) -> None:
    if type(age) not in {int, float} or not math.isfinite(age) or age < 0 or age > 86400 or (age > 0 and age < 0.1):
        raise ValueError("invalid maximum observation age")


def _observation_timestamp(row: dict[str, Any]) -> float | None:
    value = row.get("observed_at")
    if not isinstance(value, str):
        return None
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
        return parsed.timestamp() if parsed.tzinfo is not None else None
    except (ValueError, OverflowError, OSError):
        return None


def _freshness_kind(row: dict[str, Any], now: float, age: float) -> str:
    if row["status"] in {"not_observed", "read_error"}:
        return "unobserved"
    observed = _observation_timestamp(row)
    if observed is None:
        return "missing_timestamp"
    if observed > now:
        return "future_timestamp"
    return "stale" if now - observed > age else "fresh"


def _freshness_counts(out: dict[str, Any], age: float) -> dict[str, int]:
    counts = dict.fromkeys(("fresh", "stale", "missing_timestamp", "future_timestamp", "unobserved"), 0)
    now = datetime.now(UTC).timestamp()
    for row in out["items"]:
        counts[_freshness_kind(row, now, age)] += 1
    return counts


def _observations_fresh(out: dict[str, Any], age: float) -> bool:
    return age == 0 or _freshness_counts(out, age)["fresh"] == len(out["items"])


_DIAGNOSTIC_READ_MAX = 50
_DIAGNOSTIC_REFRESH_RESERVE = 1


def _diagnostic_target(row: dict[str, Any]) -> bool:
    return bool(
        row.get("selection")
        and row["status"] != "unsupported_recipient"
        and row["selection"]["reviewed"].get("reason") != "unsupported_recipient"
    )


def _diagnostic_queryable(row: dict[str, Any]) -> bool:
    return _diagnostic_target(row) and row["status"] not in {"not_observed", "read_error"}


def _valid_diagnostic(diagnostic: Any, row: dict[str, Any]) -> bool:
    if not isinstance(diagnostic, dict) or not row.get("selection") or diagnostic.get("coverage") != "exact_recipient":
        return False
    observed = diagnostic.get("observed_at")
    if not isinstance(observed, str) or not _same_acceptance(observed, observed):
        return False
    status = diagnostic.get("status")
    if status == "unavailable":
        return not diagnostic.get("recipient") and diagnostic.get("reason") in {
            "receipt_unavailable",
            "snapshot_unavailable",
            "recipient_unavailable",
            "routing_evidence_unavailable",
        }
    if status == "acceptance_replaced":
        return not diagnostic.get("recipient") and diagnostic.get("reason") == "acceptance_replaced"
    if status == "not_waiting":
        return not diagnostic.get("recipient") and diagnostic.get("routing_state") in {"enqueued", "filtered", "failed"}
    if status != "available":
        return False
    recipient = diagnostic.get("recipient")
    if not isinstance(recipient, dict):
        return False
    publication = row["selection"]["publication"]
    accepted, subscription = recipient.get("accepted_at"), recipient.get("subscription_id")
    return (
        recipient.get("event_source") == publication["source"]
        and recipient.get("event_id") == publication["id"]
        and isinstance(accepted, str)
        and _same_acceptance(accepted, publication["accepted_at"])
        and isinstance(subscription, str)
        and subscription.lower() == row["attempt"]["subscription_id"].lower()
        and recipient.get("consumer_kind") == "application"
        and recipient.get("state") == diagnostic.get("routing_state")
        and recipient.get("state") in {"pending", "processing"}
    )


def _prepare_diagnostics(
    status: dict[str, Any], previous: dict[str, Any] | None, max_diagnostic_age: float = 0
) -> dict[str, Any]:
    _validate_observation_age(max_diagnostic_age)
    out, prior = copy.deepcopy(status), copy.deepcopy(previous if previous is not None else status)
    if (
        out["source_id"].lower() != prior["source_id"].lower()
        or out.get("subscription_id", "").lower() != prior.get("subscription_id", "").lower()
        or len(out["items"]) != len(prior["items"])
        or any(
            not row.get("selection")
            or not old.get("selection")
            or not _same_selection(row["selection"], old["selection"])
            or not _same_json(row["attempt"], old["attempt"])
            for row, old in zip(out["items"], prior["items"], strict=True)
        )
    ):
        raise ValueError("saved diagnostic selection mismatch")
    saved_progress = prior.get("diagnostics_progress")
    if not isinstance(saved_progress, dict):
        saved_progress = {}

    def cursor(name: str) -> int:
        value = saved_progress.get(name)
        return value if type(value) is int and 0 <= value < len(out["items"]) else 0

    out["diagnostics_progress"] = {
        "next_index": cursor("next_index"),
        "retry_next_index": cursor("retry_next_index"),
        "refresh_next_index": cursor("refresh_next_index"),
        "reads": 0,
        "max_diagnostic_age_ms": max_diagnostic_age * 1000,
    }
    for row, old in zip(out["items"], prior["items"], strict=True):
        diagnostic = old.get("diagnostics")
        row["diagnostics"] = {"status": "budget_exhausted"}
        if not _diagnostic_target(row):
            row["diagnostics"]["status"] = "unsupported_recipient"
            continue
        if _valid_diagnostic(diagnostic, row):
            row["diagnostics"] = {**diagnostic, "retained": True}
            row["diagnostics"].pop("truncated", None)
            _set_diagnostic_comparison(row["diagnostics"], diagnostic.get("previous_observation"), row)
        elif isinstance(diagnostic, dict) and diagnostic.get("status") == "read_error":
            row["diagnostics"] = {"status": "read_error"}
            if isinstance(diagnostic.get("reason"), str):
                row["diagnostics"]["reason"] = diagnostic["reason"]
        if not row["diagnostics"].get("retained"):
            _set_diagnostic_comparison(row["diagnostics"], None, row)
        if not _diagnostic_queryable(row) and row["diagnostics"]["status"] == "budget_exhausted":
            row["diagnostics"] = {"status": "deferred", "reason": "receipt_evidence_unavailable"}
    _recount_diagnostics(out)
    return out


def _recount_diagnostics(out: dict[str, Any]) -> None:
    progress = out["diagnostics_progress"]
    progress.update(
        freshness_checked_at=datetime.now(UTC).isoformat(),
        freshness={"fresh": 0, "stale": 0, "unread": 0},
        compared=0,
        changed=0,
        target_count=0,
        remaining=0,
        deferred=0,
        retained=0,
        counts=dict.fromkeys(
            (
                "available",
                "not_waiting",
                "unavailable",
                "acceptance_replaced",
                "read_error",
                "budget_exhausted",
                "deferred",
                "unsupported_recipient",
            ),
            0,
        ),
    )
    checked = datetime.fromisoformat(progress["freshness_checked_at"]).timestamp()
    age = progress["max_diagnostic_age_ms"] / 1000
    for row in out["items"]:
        diagnostic = row["diagnostics"]
        progress["counts"][diagnostic["status"]] += 1
        if not _diagnostic_target(row):
            continue
        if not diagnostic.get("retained") and diagnostic.get("comparison") == "compared":
            progress["compared"] += 1
            progress["changed"] += bool(diagnostic.get("changes"))
        progress["target_count"] += 1
        progress["freshness"][_diagnostic_freshness(row, checked, age)] += 1
        if not _diagnostic_queryable(row):
            progress["deferred"] += 1
        if not _valid_diagnostic(diagnostic, row):
            progress["remaining"] += 1
        elif diagnostic.get("retained"):
            progress["retained"] += 1
    progress["complete"] = progress["remaining"] == 0
    progress["fresh_complete"] = progress["freshness"]["fresh"] == progress["target_count"]
    progress["consumers"] = _diagnostic_consumers(out)


async def adiagnose_commit_recovery_status(
    client: AuthenticatedClient,
    status: dict[str, Any],
    *,
    previous: dict[str, Any] | None = None,
    max_diagnostic_age: float = 0,
    strict_reads: bool = False,
) -> dict[str, Any]:
    """Rotate bounded exact lookups; previous must bind to the validated selection."""
    out = _prepare_diagnostics(status, previous, max_diagnostic_age)
    progress = out["diagnostics_progress"]
    baselines = [_diagnostic_observation(row["diagnostics"], row) for row in out["items"]]

    def candidates(start: int, group: int) -> list[int]:
        indices = []
        for n in range(len(out["items"])):
            index = (start + n) % len(out["items"])
            row = out["items"][index]
            kind = (
                2
                if _valid_diagnostic(row["diagnostics"], row)
                else 1
                if row["diagnostics"]["status"] == "read_error"
                else 0
            )
            if _diagnostic_queryable(row) and kind == group:
                indices.append(index)
        return indices

    unread = candidates(progress["next_index"], 0)[:_DIAGNOSTIC_READ_MAX]
    remaining = _DIAGNOSTIC_READ_MAX - len(unread)
    failures, successes = candidates(progress["retry_next_index"], 1), candidates(progress["refresh_next_index"], 2)
    reserve = _DIAGNOSTIC_REFRESH_RESERVE if successes and failures and remaining > _DIAGNOSTIC_REFRESH_RESERVE else 0
    failed = failures[: remaining - reserve]
    remaining -= len(failed)
    if max_diagnostic_age > 0:
        now = datetime.now(UTC).timestamp()
        successes.sort(key=lambda index: _diagnostic_freshness(out["items"][index], now, max_diagnostic_age) != "stale")
    covered = successes[:remaining]
    selected = [
        (cursor_name, index)
        for cursor_name, indices in zip(
            ("next_index", "retry_next_index", "refresh_next_index"), (unread, failed, covered), strict=True
        )
        for index in indices
    ]
    queries = []
    for _, index in selected:
        row = out["items"][index]
        publication = row["selection"]["publication"]
        queries.append(
            {
                "source": publication["source"],
                "id": publication["id"],
                "subscription_id": row["attempt"]["subscription_id"].lower(),
                "expected_accepted_at": publication["accepted_at"],
            }
        )
    batch = None
    interrupted = None
    transport_failure = None
    if selected:
        for cursor_name, index in selected:
            out["items"][index]["diagnostics"] = {"status": "read_error"}
            progress["reads"] += 1
            progress[cursor_name] = (index + 1) % len(out["items"])
        try:
            response = await get_event_recipient_blockers.asyncio_detailed(
                client=client, body=EventRecipientBlockerBatchRequest.from_dict({"queries": queries})
            )
            if response.status_code != 200:
                raise CommitDeliveryReadError(response.status_code, response.headers)
            # Read original JSON to preserve nanosecond acceptance guards.
            batch = (
                json.loads(response.content)
                if isinstance(response.parsed, EventRecipientBlockerBatchResponse)
                else None
            )
        except (asyncio.CancelledError, TimeoutError) as error:
            interrupted = error
        except Exception as error:
            transport_failure = error
    for n, (_, index) in enumerate(selected):
        row = out["items"][index]
        publication, subscription = row["selection"]["publication"], row["attempt"]["subscription_id"].lower()
        diagnostic = row["diagnostics"]
        if batch is None:
            continue
        items = batch.get("items")
        if not isinstance(items, list) or len(items) != len(selected) or not isinstance(items[n], dict):
            diagnostic["reason"] = "invalid_evidence"
            continue
        item = items[n]
        echoed = item.get("query")
        query_expected = queries[n]
        if (
            not isinstance(echoed, dict)
            or any(echoed.get(key) != query_expected[key] for key in ("source", "id", "subscription_id"))
            or not isinstance(echoed.get("expected_accepted_at"), str)
            or not _same_acceptance(echoed["expected_accepted_at"], query_expected["expected_accepted_at"])
        ):
            diagnostic["reason"] = "invalid_evidence"
            continue
        if item.get("error"):
            continue
        result = item.get("result")
        if not isinstance(result, dict):
            diagnostic["reason"] = "invalid_evidence"
            continue
        query = result.get("query")
        if not isinstance(query, dict):
            diagnostic["reason"] = "invalid_evidence"
            continue
        expected = query.get("expected_accepted_at")
        if (
            query.get("source") != publication["source"]
            or query.get("id") != publication["id"]
            or query.get("subscription_id") != subscription
            or not isinstance(expected, str)
            or not _same_acceptance(expected, publication["accepted_at"])
        ):
            diagnostic["reason"] = "invalid_evidence"
            continue
        sample = {
            "status": result.get("status"),
            "observed_at": result.get("observed_at"),
            "coverage": "exact_recipient",
        }
        for name in ("reason", "routing_state", "recipient"):
            if result.get(name) is not None:
                sample[name] = result[name]
        row["diagnostics"] = (
            sample if _valid_diagnostic(sample, row) else {"status": "read_error", "reason": "invalid_evidence"}
        )
    for row, baseline in zip(out["items"], baselines, strict=True):
        if not row["diagnostics"].get("retained"):
            _set_diagnostic_comparison(row["diagnostics"], baseline, row)
    if interrupted is not None:
        _recount_diagnostics(out)
        raise CommitRecoveryStatusError(out) from interrupted
    _recount_diagnostics(out)
    if strict_reads and transport_failure is not None:
        raise CommitRecoveryStatusError(out) from transport_failure
    return out


def _diagnostic_freshness(row: dict[str, Any], now: float, age: float) -> str:
    if not _valid_diagnostic(row.get("diagnostics"), row):
        return "unread"
    try:
        observed = datetime.fromisoformat(row["diagnostics"]["observed_at"].replace("Z", "+00:00")).timestamp()
    except (ValueError, OverflowError, TypeError):
        return "stale" if age > 0 else "fresh"
    return "stale" if age > 0 and (observed > now or now - observed > age) else "fresh"


_DIAGNOSTIC_WAITING_REASONS = {
    "circuit_open",
    "circuit_probe_wait",
    "circuit_recovery_rate_limited",
    "subscription_paused",
    "subscription_rate_limited",
    "ordering_blocked",
    "capacity_consumer",
    "capacity_app",
    "capacity_account",
    "routing_in_progress",
    "receipt_processing",
    "retry_backoff",
    "workflow_routing",
    "ready",
}


def _diagnostic_observation(diagnostic: Any, row: dict[str, Any]) -> dict[str, Any] | None:
    if not _valid_diagnostic(diagnostic, row) or diagnostic.get("status") not in {"available", "not_waiting"}:
        return None
    publication = row["selection"]["publication"]
    observation = {
        "query": {
            "source": publication["source"],
            "id": publication["id"],
            "subscription_id": row["attempt"]["subscription_id"].lower(),
            "expected_accepted_at": publication["accepted_at"],
        },
        "observed_at": diagnostic["observed_at"],
        "status": diagnostic["status"],
        "routing_state": diagnostic.get("routing_state", ""),
    }
    recipient = diagnostic.get("recipient")
    if recipient:
        routing = {
            "waiting_reason": recipient.get("waiting_reason"),
            "target_available": recipient.get("target_available"),
        }
        if recipient.get("capacity_scope"):
            routing["capacity_scope"] = recipient["capacity_scope"]
        if recipient.get("ordering_blocker"):
            routing["ordering_blocker"] = {
                key: recipient["ordering_blocker"].get(key)
                for key in ("event_source", "event_id", "subscription_id", "accepted_at", "state")
            }
        observation["routing"] = routing
    return observation if _valid_diagnostic_observation(observation, observation["query"]) else None


def _valid_diagnostic_observation(observation: Any, query: dict[str, Any]) -> bool:
    if not isinstance(observation, dict) or not isinstance(observation.get("query"), dict):
        return False
    saved = observation["query"]
    if (
        any(saved.get(key) != query[key] for key in ("source", "id", "subscription_id"))
        or not isinstance(saved.get("expected_accepted_at"), str)
        or not _same_acceptance(saved["expected_accepted_at"], query["expected_accepted_at"])
    ):
        return False
    observed = observation.get("observed_at")
    if not isinstance(observed, str) or not _same_acceptance(observed, observed):
        return False
    if observation.get("status") == "not_waiting":
        return observation.get("routing") is None and observation.get("routing_state") in (
            "enqueued",
            "filtered",
            "failed",
        )
    routing = observation.get("routing")
    if (
        observation.get("status") != "available"
        or not isinstance(routing, dict)
        or observation.get("routing_state") not in ("pending", "processing")
    ):
        return False
    if (
        type(routing.get("target_available")) is not bool
        or not isinstance(routing.get("waiting_reason"), str)
        or routing["waiting_reason"] not in _DIAGNOSTIC_WAITING_REASONS
        or routing.get("capacity_scope", "") not in ("", "consumer", "app", "account")
    ):
        return False
    blocker = routing.get("ordering_blocker")
    if blocker is None:
        return True
    if not isinstance(blocker, dict):
        return False
    for key in ("event_source", "event_id", "subscription_id"):
        value = blocker.get(key)
        if not isinstance(value, str) or not value:
            return False
        try:
            if len(value.encode("utf-8")) > 256:
                return False
        except UnicodeEncodeError:
            return False
    accepted = blocker.get("accepted_at")
    return (
        blocker["subscription_id"].lower() == query["subscription_id"].lower()
        and isinstance(accepted, str)
        and _same_acceptance(accepted, accepted)
        and blocker.get("state") in ("pending", "processing")
    )


def _clean_diagnostic_observation(observation: dict[str, Any]) -> dict[str, Any]:
    clean = {key: observation[key] for key in ("observed_at", "status", "routing_state")}
    clean["query"] = {
        key: observation["query"][key] for key in ("source", "id", "subscription_id", "expected_accepted_at")
    }
    routing = observation.get("routing")
    if routing is not None:
        clean["routing"] = {
            key: routing[key] for key in ("waiting_reason", "capacity_scope", "target_available") if key in routing
        }
        if routing.get("ordering_blocker") is not None:
            clean["routing"]["ordering_blocker"] = {
                key: routing["ordering_blocker"][key]
                for key in ("event_source", "event_id", "subscription_id", "accepted_at", "state")
            }
    return clean


def _set_diagnostic_comparison(diagnostic: dict[str, Any], previous: Any, row: dict[str, Any]) -> None:
    diagnostic.pop("previous_observation", None)
    diagnostic.pop("changes", None)
    diagnostic["comparison"] = "evidence_unavailable"
    current = _diagnostic_observation(diagnostic, row)
    if current is None:
        return
    diagnostic["comparison"] = "baseline_unavailable"
    if not _valid_diagnostic_observation(previous, current["query"]):
        return
    diagnostic["comparison"] = "non_monotonic_timestamp"
    if _diagnostic_time_key(current["observed_at"]) <= _diagnostic_time_key(previous["observed_at"]):
        return
    diagnostic["comparison"] = "compared"
    diagnostic["previous_observation"] = _clean_diagnostic_observation(previous)
    before, after = _diagnostic_routing_fields(previous), _diagnostic_routing_fields(current)
    fields = ["status", "routing_state"]
    if previous["status"] == "available" and current["status"] == "available":
        fields += [
            "waiting_reason",
            "capacity_scope",
            "target_available",
            "ordering_source",
            "ordering_id",
            "ordering_subscription_id",
            "ordering_accepted_at",
            "ordering_state",
        ]
    changes = []
    for field in fields:
        left, right = before.get(field, ""), after.get(field, "")
        if left == right or (field == "ordering_accepted_at" and left and right and _same_acceptance(left, right)):
            continue
        changes.append({"field": field, "previous": left, "current": right})
    if changes:
        diagnostic["changes"] = changes


def _diagnostic_time_key(value: str) -> tuple[datetime, str]:
    match = re.fullmatch(r"(.+?)(?:\.([0-9]{1,9}))?(Z|[+-][0-9]{2}:[0-9]{2})", value)
    if match is None:
        raise ValueError("invalid diagnostic observation timestamp")
    return datetime.fromisoformat(match[1] + match[3]), (match[2] or "").ljust(9, "0")


def _diagnostic_routing_fields(observation: dict[str, Any]) -> dict[str, str]:
    fields = {key: observation[key] for key in ("status", "routing_state")}
    routing = observation.get("routing")
    if routing:
        fields.update(
            waiting_reason=routing["waiting_reason"],
            capacity_scope=routing.get("capacity_scope", ""),
            target_available=str(routing["target_available"]).lower(),
        )
        blocker = routing.get("ordering_blocker")
        if blocker:
            fields.update(
                ordering_source=blocker["event_source"],
                ordering_id=blocker["event_id"],
                ordering_subscription_id=blocker["subscription_id"].lower(),
                ordering_accepted_at=blocker["accepted_at"],
                ordering_state=blocker["state"],
            )
    return fields


def _diagnostic_coverage_complete(out: dict[str, Any], age: float) -> bool:
    progress = out.get("diagnostics_progress")
    if not isinstance(progress, dict):
        return False
    progress["max_diagnostic_age_ms"] = age * 1000
    _recount_diagnostics(out)
    return progress["complete"] and progress["fresh_complete"]


class _DiagnosticEstimator:
    def __init__(self) -> None:
        self.passes = 0
        self.samples = 0
        self.read_ms = 0.0

    def observe(self, out: dict[str, Any], read_ms: float) -> None:
        if not out.get("diagnostics_progress", {}).get("reads"):
            return
        self.passes += 1
        self.read_ms += read_ms
        self.samples += sum(
            _diagnostic_target(row)
            and not row.get("diagnostics", {}).get("retained")
            and _valid_diagnostic(row.get("diagnostics"), row)
            for row in out["items"]
        )

    def estimate(self, out: dict[str, Any], elapsed_ms: float, delay_ms: float) -> dict[str, Any]:
        progress = out["diagnostics_progress"]
        targets = [row for row in out["items"] if _diagnostic_target(row)]
        unread = [row for row in targets if not _valid_diagnostic(row.get("diagnostics"), row)]
        result = {
            "state": "insufficient_data",
            "target_count": len(targets),
            "remaining": len(unread),
            "deferred": sum(not _diagnostic_queryable(row) for row in unread),
            "diagnostic_passes": self.passes,
            "successful_samples": self.samples,
            "samples_per_second": self.samples * 1000 / elapsed_ms if elapsed_ms > 0 else 0,
            "samples_per_pass": self.samples / self.passes if self.passes > 0 else 0,
            "refresh_exceeds_max_age": False,
        }
        if not result["remaining"]:
            result.update(state="covered", estimated_remaining_passes=0, estimated_remaining_ms=0)
        elif result["deferred"]:
            result["state"] = "awaiting_receipts"
        if result["deferred"] or not self.passes or not self.samples or elapsed_ms <= 0:
            return result
        cycle_ms = (elapsed_ms + delay_ms) / self.passes
        refresh_passes = math.ceil(result["target_count"] / result["samples_per_pass"])
        result["estimated_refresh_ms"] = (
            math.ceil((refresh_passes - 1) * cycle_ms + self.read_ms / self.passes) if refresh_passes > 0 else 0
        )
        result["refresh_exceeds_max_age"] = (
            progress["max_diagnostic_age_ms"] > 0 and result["estimated_refresh_ms"] > progress["max_diagnostic_age_ms"]
        )
        if result["remaining"] and not result["deferred"]:
            result.update(
                state="estimated",
                estimated_remaining_passes=math.ceil(result["remaining"] / result["samples_per_pass"]),
            )
            result["estimated_remaining_ms"] = math.ceil(result["estimated_remaining_passes"] * cycle_ms)
        return result


def _diagnostic_consumers(out: dict[str, Any]) -> list[dict[str, Any]]:
    p = out["diagnostics_progress"]
    groups: dict[str, dict[str, Any]] = {}
    checked = datetime.fromisoformat(p["freshness_checked_at"]).timestamp()
    for row in out["items"]:
        if not _diagnostic_target(row):
            continue
        sid = row["attempt"]["subscription_id"].lower()
        g = groups.setdefault(
            sid,
            dict(
                subscription_id=sid,
                target_count=0,
                freshness=dict(fresh=0, stale=0, unread=0),
                statuses={},
                fresh_waiting_reasons={},
                stale_waiting_reasons={},
                compared=0,
                changed=0,
            ),
        )
        g["target_count"] += 1
        freshness = _diagnostic_freshness(row, checked, p["max_diagnostic_age_ms"] / 1000)
        g["freshness"][freshness] += 1
        d = row.get("diagnostics")
        if not _valid_diagnostic(d, row):
            continue
        g["statuses"][d["status"]] = g["statuses"].get(d["status"], 0) + 1
        if not d.get("retained") and d.get("comparison") == "compared":
            g["compared"] += 1
            g["changed"] += bool(d.get("changes"))
        if d["status"] != "available":
            continue
        reason = d["recipient"]["waiting_reason"]
        reasons = g["stale_waiting_reasons"] if freshness == "stale" else g["fresh_waiting_reasons"]
        reasons[reason] = reasons.get(reason, 0) + 1
        pub = row["selection"]["publication"]
        oldest = g.get("oldest_waiting")
        if oldest is None or _diagnostic_time_key(pub["accepted_at"]) < _diagnostic_time_key(
            oldest["query"]["expected_accepted_at"]
        ):
            g["oldest_waiting"] = dict(
                query=dict(
                    source=pub["source"], id=pub["id"], subscription_id=sid, expected_accepted_at=pub["accepted_at"]
                ),
                observed_at=d["observed_at"],
                freshness=freshness,
                waiting_reason=reason,
            )
    return [groups[sid] for sid in sorted(groups)]


_PERSISTENT_BLOCKERS = frozenset(
    (
        "subscription_paused",
        "subscription_rate_limited",
        "circuit_open",
        "circuit_probe_wait",
        "circuit_recovery_rate_limited",
        "ordering_blocked",
        "capacity_consumer",
        "capacity_app",
        "capacity_account",
        "retry_backoff",
    )
)


class _StallTracker:
    def __init__(self) -> None:
        self.samples: dict[int, dict[str, Any]] = {}

    def observe(self, out: dict[str, Any], age: float, threshold: float, now: float) -> None:
        if threshold == 0:
            return
        wall = datetime.now(UTC).timestamp()
        for i, row in enumerate(out["items"]):
            previous = self.samples.get(i)
            current = _diagnostic_observation(row.get("diagnostics"), row)
            if current is None or _diagnostic_freshness(row, wall, age) != "fresh":
                self.samples.pop(i, None)
                continue
            if previous:
                observed = datetime.fromisoformat(previous["observation"]["observed_at"]).timestamp()
                if observed > wall or wall - observed > age:
                    self.samples.pop(i, None)
                    previous = None
            if row["diagnostics"].get("retained"):
                continue
            sample = dict(
                observation=current,
                first=current["observed_at"],
                started=now,
                last=now,
                samples=1,
                advancing=False,
                changed=False,
            )
            if previous and _diagnostic_time_key(current["observed_at"]) > _diagnostic_time_key(
                previous["observation"]["observed_at"]
            ):
                before = _diagnostic_routing_fields(previous["observation"])
                after = _diagnostic_routing_fields(current)
                if (
                    before.get("ordering_accepted_at")
                    and after.get("ordering_accepted_at")
                    and _diagnostic_time_key(before["ordering_accepted_at"])
                    == _diagnostic_time_key(after["ordering_accepted_at"])
                ):
                    after["ordering_accepted_at"] = before["ordering_accepted_at"]
                if before == after:
                    sample.update(first=previous["first"], started=previous["started"], samples=previous["samples"] + 1)
                else:
                    sample["changed"] = True
                    prior = previous["observation"]
                    sample["advancing"] = (
                        prior["routing_state"] == "pending" and current["routing_state"] == "processing"
                    ) or (
                        prior["status"] == "available"
                        and current["status"] == "not_waiting"
                        and current["routing_state"] == "enqueued"
                    )
            elif previous:
                self.samples.pop(i, None)
                continue
            self.samples[i] = sample

    def summarize(self, out: dict[str, Any], age: float, threshold: float) -> list[dict[str, Any]]:
        if threshold == 0 or "diagnostics_progress" not in out:
            return []
        groups = {
            c["subscription_id"]: dict(
                subscription_id=c["subscription_id"],
                counts=dict.fromkeys(
                    ("insufficient_evidence", "advancing", "routing_changed", "not_blocked", "observing", "stalled"), 0
                ),
                blockers=[],
            )
            for c in out["diagnostics_progress"]["consumers"]
        }
        wall = datetime.now(UTC).timestamp()
        for i, row in enumerate(out["items"]):
            if not _diagnostic_target(row):
                continue
            g = groups[row["attempt"]["subscription_id"].lower()]
            sample = self.samples.get(i)
            if (
                sample is None
                or _diagnostic_freshness(row, wall, age) != "fresh"
                or _diagnostic_time_key(row["diagnostics"]["observed_at"])
                != _diagnostic_time_key(sample["observation"]["observed_at"])
            ):
                self.samples.pop(i, None)
                g["counts"]["insufficient_evidence"] += 1
                continue
            if sample["changed"]:
                g["counts"]["routing_changed"] += 1
            if sample["advancing"]:
                g["counts"]["advancing"] += 1
            o = sample["observation"]
            if not o.get("routing") or o["routing"]["waiting_reason"] not in _PERSISTENT_BLOCKERS:
                g["counts"]["not_blocked"] += 1
                continue
            duration = max(0, (sample["last"] - sample["started"]) * 1000)
            state = "stalled" if sample["samples"] >= 2 and duration >= threshold * 1000 else "observing"
            g["counts"][state] += 1
            g["blockers"].append(
                dict(
                    query=o["query"],
                    waiting_reason=o["routing"]["waiting_reason"],
                    state=state,
                    first_observed_at=sample["first"],
                    last_observed_at=o["observed_at"],
                    samples=sample["samples"],
                    monitored_ms=int(duration),
                )
            )
        return list(groups.values())


class _IncidentTracker:
    def __init__(self, durable: bool = False) -> None:
        self.durable = durable
        self.queue_full = False
        self.pending: list[dict[str, Any]] = []
        self.session = uuid4().hex
        self.sequence = 0
        self.active: dict[int, dict[str, Any]] = {}
        self.events: list[dict[str, Any]] = []

    def can_open(self) -> bool:
        if not self.durable:
            return True
        if self.queue_full:
            return False
        if (
            len(self.pending) + len(self.active) + 2 > _ALERT_QUEUE_MAX
            or self.sequence + len(self.active) + 2 > _ALERT_SEQUENCE_MAX
        ):
            self.queue_full = True
            return False
        return True

    def acknowledge(self, acks: list[dict[str, Any]]) -> None:
        if not isinstance(acks, list) or len(acks) > _ALERT_QUEUE_MAX or (not self.durable and acks):
            raise ValueError("invalid alert acknowledgements")
        known = {(e["incident_id"], e["sequence"]) for e in self.pending}
        pending_sequences = {e["sequence"] for e in self.pending}
        selected: set[tuple[str, int]] = set()
        for ack in acks:
            if (
                not isinstance(ack, dict)
                or not isinstance(ack.get("incident_id"), str)
                or type(ack.get("sequence")) is not int
            ):
                raise ValueError("invalid alert acknowledgement")
            key = ack["incident_id"], ack["sequence"]
            opened = _alert_incident_sequence(ack["incident_id"], self.session)
            already_removed = (
                opened is not None
                and opened <= ack["sequence"] <= self.sequence
                and ack["sequence"] not in pending_sequences
            )
            if key in selected or (key not in known and not already_removed):
                raise ValueError("acknowledgement does not match this alert stream")
            selected.add(key)
        self.pending = [e for e in self.pending if (e["incident_id"], e["sequence"]) not in selected]

    def outbox(self, active: list[dict[str, Any]]) -> dict[str, Any]:
        return dict(
            version=1, session=self.session, sequence=self.sequence, pending=list(self.pending), active=list(active)
        )

    def restore(self, previous: dict[str, Any] | None, out: dict[str, Any], acks: list[dict[str, Any]]) -> None:
        saved = previous.get("monitoring", {}).get("alert_outbox") if previous is not None else None
        if saved is None:
            self.acknowledge(acks)
            return
        if not self.durable:
            raise ValueError("resuming pending alert state requires durable alerts")
        if (
            out["source_id"].lower() != previous["source_id"].lower()
            or out.get("subscription_id", "").lower() != previous.get("subscription_id", "").lower()
            or len(out["items"]) != len(previous["items"])
            or any(
                not row.get("selection")
                or not old.get("selection")
                or not _same_selection(row["selection"], old["selection"])
                or not _same_json(row["attempt"], old["attempt"])
                for row, old in zip(out["items"], previous["items"], strict=True)
            )
        ):
            raise ValueError("saved alert selection mismatch")
        normalized = _validate_alert_outbox(saved, out)
        staged = _IncidentTracker(True)
        staged.session, staged.sequence = normalized["session"], normalized["sequence"]
        staged.pending = normalized["pending"]
        staged.acknowledge(acks)
        self.session, self.sequence, self.pending = normalized["session"], normalized["sequence"], staged.pending
        for incident in normalized["active"]:
            self.emit(incident, "confirmation_lost", "watch_resumed", None)

    def begin_pass(self) -> None:
        self.events = []

    def emit(self, incident: dict[str, Any], transition: str, reason: str, evidence: dict[str, Any] | None) -> None:
        self.sequence += 1
        event = dict(
            sequence=self.sequence,
            incident_id=incident["incident_id"],
            transition=transition,
            reason=reason,
            at=datetime.now(UTC).isoformat(),
            query=incident["query"],
            waiting_reason=incident["waiting_reason"],
            **({"evidence": evidence} if evidence is not None else {}),
        )
        self.events.append(event)
        if self.durable:
            self.pending.append(event)

    @staticmethod
    def key(query: dict[str, Any]) -> tuple[str, str, str, str]:
        return query["source"], query["id"], query["subscription_id"].lower(), query["expected_accepted_at"]

    def summarize(
        self, out: dict[str, Any], consumers: list[dict[str, Any]], age: float, threshold: float
    ) -> dict[str, Any]:
        if threshold == 0:
            return dict(blocker_incidents=[], blocker_transitions=[])
        waiting = {self.key(b["query"]): b for c in consumers for b in c["blockers"] if b["state"] == "stalled"}
        now = datetime.now(UTC)
        seen: set[int] = set()
        for i, row in enumerate(out["items"]):
            if not _diagnostic_target(row):
                continue
            seen.add(i)
            pub = row["selection"]["publication"]
            query = dict(
                source=pub["source"],
                id=pub["id"],
                subscription_id=row["attempt"]["subscription_id"].lower(),
                expected_accepted_at=pub["accepted_at"],
            )
            blocker = waiting.get(self.key(query))
            active = self.active.get(i)
            current = _diagnostic_observation(row.get("diagnostics"), row)
            fresh = current is not None and _diagnostic_freshness(row, now.timestamp(), age) == "fresh"
            if active:
                last = datetime.fromisoformat(active["incident"]["last_observed_at"]).timestamp()
                uninterrupted = last <= now.timestamp() and now.timestamp() - last <= age
                same_episode = blocker is not None and _diagnostic_time_key(
                    blocker["first_observed_at"]
                ) == _diagnostic_time_key(active["first"])
                if not fresh or not uninterrupted or not same_episode:
                    transition, reason = "confirmation_lost", "episode_reset"
                    if not fresh:
                        reason = "evidence_unavailable"
                    elif not uninterrupted:
                        reason = "sample_gap"
                    elif (
                        not row["diagnostics"].get("retained")
                        and _diagnostic_time_key(current["observed_at"])
                        > _diagnostic_time_key(active["incident"]["last_observed_at"])
                        and (
                            current["status"] == "not_waiting"
                            or (
                                current.get("routing")
                                and current["routing"]["waiting_reason"] not in _PERSISTENT_BLOCKERS
                            )
                        )
                    ):
                        transition, reason = "resolved", "routing_not_blocked"
                    self.emit(active["incident"], transition, reason, current if fresh else None)
                    self.active.pop(i, None)
                    active = None
            if blocker is not None and fresh:
                if active is None:
                    if not self.can_open():
                        continue
                    incident = dict(
                        incident_id=self.session + "-" + str(self.sequence + 1),
                        query=query,
                        waiting_reason=blocker["waiting_reason"],
                        opened_at=now.isoformat(),
                        last_observed_at=current["observed_at"],
                    )
                    active = dict(incident=incident, first=blocker["first_observed_at"])
                    self.active[i] = active
                    self.emit(incident, "opened", "persistent_blocker", current)
                active["incident"]["last_observed_at"] = current["observed_at"]
        for i in sorted(self.active):
            if i not in seen:
                self.emit(self.active[i]["incident"], "confirmation_lost", "selection_unavailable", None)
                self.active.pop(i)
        return dict(
            blocker_incidents=[dict(self.active[i]["incident"]) for i in sorted(self.active)],
            blocker_transitions=list(self.events),
        )


_ALERT_QUEUE_MAX = 1024
_ALERT_SEQUENCE_MAX = 9007199254740991


def _alert_incident_sequence(value: Any, session: str) -> int | None:
    if not isinstance(value, str) or not value.startswith(session + "-"):
        return None
    text = value[len(session) + 1 :]
    if len(text) > 16 or re.fullmatch(r"[1-9][0-9]*", text) is None:
        return None
    number = int(text)
    return number if number <= _ALERT_SEQUENCE_MAX else None


def _valid_alert_time(value: Any) -> bool:
    return isinstance(value, str) and _same_acceptance(value, value)


def _validate_alert_outbox(saved: Any, out: dict[str, Any]) -> dict[str, Any]:
    if not isinstance(saved, dict):
        raise ValueError("invalid saved alert outbox")
    pending = [] if saved.get("pending") is None else saved["pending"]
    active = [] if saved.get("active") is None else saved["active"]
    session, sequence = saved.get("session"), saved.get("sequence")
    if (
        type(saved.get("version")) is not int
        or saved["version"] != 1
        or not isinstance(session, str)
        or re.fullmatch(r"[0-9a-f]{32}", session) is None
        or type(sequence) is not int
        or not 0 <= sequence <= _ALERT_SEQUENCE_MAX
        or not isinstance(pending, list)
        or not isinstance(active, list)
        or len(pending) + len(active) > _ALERT_QUEUE_MAX
        or len(active) > _ALERT_SEQUENCE_MAX - sequence
    ):
        raise ValueError("invalid saved alert outbox bounds")

    def bound_query(query: Any) -> dict[str, Any]:
        if (
            not isinstance(query, dict)
            or any(
                not isinstance(query.get(k), str) or not query[k] or len(query[k].encode("utf-8")) > 256
                for k in ("source", "id", "subscription_id")
            )
            or not _valid_alert_time(query.get("expected_accepted_at"))
        ):
            raise ValueError("invalid alert query")
        row = next(
            (
                r
                for r in out["items"]
                if _diagnostic_target(r)
                and r["selection"]["publication"]["source"] == query["source"]
                and r["selection"]["publication"]["id"] == query["id"]
                and r["attempt"]["subscription_id"].lower() == query["subscription_id"].lower()
                and _same_acceptance(r["selection"]["publication"]["accepted_at"], query["expected_accepted_at"])
            ),
            None,
        )
        if row is None:
            raise ValueError("saved alert query does not match selection")
        return dict(
            source=query["source"],
            id=query["id"],
            subscription_id=query["subscription_id"],
            expected_accepted_at=row["selection"]["publication"]["accepted_at"],
        )

    lineage: dict[str, tuple[Any, ...]] = {}
    closed: set[str] = set()
    clean_pending, last = [], 0
    for event in pending:
        if not isinstance(event, dict):
            raise ValueError("invalid saved pending alert")
        opened = _alert_incident_sequence(event.get("incident_id"), session)
        query = bound_query(event.get("query"))
        evidence, seq = event.get("evidence"), event.get("sequence")
        if (
            opened is None
            or type(seq) is not int
            or not last < seq <= sequence
            or opened > seq
            or not _valid_alert_time(event.get("at"))
            or event.get("waiting_reason") not in _PERSISTENT_BLOCKERS
            or (evidence is not None and not _valid_diagnostic_observation(evidence, query))
        ):
            raise ValueError("invalid saved pending alert")
        transition, reason = event.get("transition"), event.get("reason")
        if transition == "opened":
            valid = (
                opened == seq
                and reason == "persistent_blocker"
                and evidence is not None
                and evidence["status"] == "available"
                and evidence["routing"]["waiting_reason"] == event["waiting_reason"]
            )
        elif transition == "resolved":
            valid = (
                reason == "routing_not_blocked"
                and evidence is not None
                and (
                    evidence["status"] == "not_waiting"
                    or evidence["routing"]["waiting_reason"] not in _PERSISTENT_BLOCKERS
                )
            )
        else:
            valid = transition == "confirmation_lost" and reason in (
                "episode_reset",
                "evidence_unavailable",
                "sample_gap",
                "selection_unavailable",
                "watch_resumed",
            )
        if not valid:
            raise ValueError("invalid alert transition")
        identity = (
            query["source"],
            query["id"],
            query["subscription_id"].lower(),
            query["expected_accepted_at"],
            event["waiting_reason"],
        )
        if event["incident_id"] in closed or (
            event["incident_id"] in lineage and lineage[event["incident_id"]] != identity
        ):
            raise ValueError("inconsistent saved alert episode")
        lineage[event["incident_id"]] = identity
        if transition != "opened":
            closed.add(event["incident_id"])
        last = seq
        clean_pending.append(
            dict(
                sequence=seq,
                incident_id=event["incident_id"],
                transition=transition,
                reason=reason,
                at=event["at"],
                query=query,
                waiting_reason=event["waiting_reason"],
                **({"evidence": _clean_diagnostic_observation(evidence)} if evidence is not None else {}),
            )
        )
    clean_active, ids, queries = [], set(), set()
    for incident in active:
        if not isinstance(incident, dict):
            raise ValueError("invalid saved active alert")
        opened = _alert_incident_sequence(incident.get("incident_id"), session)
        query = bound_query(incident.get("query"))
        key = query["source"], query["id"], query["subscription_id"].lower(), query["expected_accepted_at"]
        if (
            opened is None
            or opened > sequence
            or incident["incident_id"] in closed
            or (
                incident["incident_id"] in lineage
                and lineage[incident["incident_id"]] != (*key, incident["waiting_reason"])
            )
            or incident["incident_id"] in ids
            or key in queries
            or not _valid_alert_time(incident.get("opened_at"))
            or not _valid_alert_time(incident.get("last_observed_at"))
            or incident.get("waiting_reason") not in _PERSISTENT_BLOCKERS
        ):
            raise ValueError("invalid saved active alert")
        ids.add(incident["incident_id"])
        queries.add(key)
        clean_active.append(
            dict(
                incident_id=incident["incident_id"],
                query=query,
                waiting_reason=incident["waiting_reason"],
                opened_at=incident["opened_at"],
                last_observed_at=incident["last_observed_at"],
            )
        )
    return dict(version=1, session=session, sequence=sequence, pending=clean_pending, active=clean_active)
