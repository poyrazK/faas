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
from uuid import UUID

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
    resume: dict[str, Any] | None = None,
    refresh: dict[str, Any] | None = None,
    on_progress: Callable[[dict[str, Any]], None] | None = None,
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
    _validate_observation_age(max_diagnostic_age)
    if max_diagnostic_age > 0 and not diagnostics:
        raise ValueError("maximum diagnostic age requires diagnostics")
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

    def summarize(stop_reason: str) -> dict[str, Any]:
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
                    if diagnostics:
                        out = await adiagnose_commit_recovery_status(
                            client,
                            out,
                            previous=diagnostic_previous,
                            max_diagnostic_age=max_diagnostic_age,
                            strict_reads=watch_diagnostics,
                        )
                except CommitRecoveryStatusError as error:
                    out = error.result
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
                out["monitoring"] = summarize("running")
                phase = "callback"
                if on_progress is not None:
                    on_progress(copy.deepcopy(out))
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
