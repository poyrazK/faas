"""Read-only reconciliation of current delivery evidence and saved recovery attempts."""

from __future__ import annotations

import asyncio
import copy
import json
import math
from collections.abc import Callable
from datetime import UTC, datetime
from email.utils import parsedate_to_datetime
from typing import Any
from uuid import UUID

import httpx

from .client import AuthenticatedClient
from .commit_delivery_report import CommitDeliveryReportError, aget_commit_delivery_report
from .commit_recovery_plan import _same_acceptance


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
    resume: dict[str, Any] | None = None,
    refresh: dict[str, Any] | None = None,
) -> dict[str, Any]:
    """Read guarded evidence only; never resolve an unknown write from delivery advancement."""
    if resume is not None and refresh is not None:
        raise ValueError("choose resume or refresh")
    source = str(UUID(str(source)))
    plan, result = copy.deepcopy(reviewed), copy.deepcopy(applied)
    previous = copy.deepcopy(resume if resume is not None else refresh)
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
    except (Exception, asyncio.CancelledError) as error:
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
    resume: dict[str, Any] | None = None,
    refresh: dict[str, Any] | None = None,
    on_progress: Callable[[dict[str, Any]], None] | None = None,
) -> dict[str, Any]:
    """Bounded polling; timeout/cancellation attaches the latest observations."""
    if (
        not math.isfinite(interval)
        or not 0.1 <= interval <= 60
        or not math.isfinite(timeout)
        or not 0 < timeout <= 86400
    ):
        raise ValueError("invalid recovery watch options")
    if type(read_retries) is not int or not 0 <= read_retries <= 20:
        raise ValueError("invalid recovery watch read retry budget")
    if resume is not None and refresh is not None:
        raise ValueError("choose resume or refresh")
    plan, result = copy.deepcopy(reviewed), copy.deepcopy(applied)
    previous = copy.deepcopy(resume if resume is not None else refresh)
    refreshing = refresh is not None or (
        previous is not None and all(row["status"] not in {"not_observed", "read_error"} for row in previous["items"])
    )
    out: dict[str, Any] = {"source_id": str(source), "items": [], "counts": {}, "remaining": 0, "complete": False}
    loop = asyncio.get_running_loop()
    deadline = loop.time() + timeout
    used_retries, passes = 0, 0
    reason, phase = "read_failure", "read"

    def summarize(stop_reason: str) -> dict[str, Any]:
        return {
            "stop_reason": stop_reason,
            "passes": passes,
            "retries_consumed": used_retries,
            "elapsed_ms": max(0, int((loop.time() - (deadline - timeout)) * 1000)),
            "acknowledged_executions_terminal": commit_recovery_status_settled(out),
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
                        resume=previous if not refreshing else None,
                        refresh=previous if refreshing else None,
                    )
                except CommitRecoveryStatusError as error:
                    out = error.result
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
                if not failed and commit_recovery_status_settled(out):
                    out["monitoring"] = summarize("settled")
                    return out
                previous, refreshing = out, not failed and out["remaining"] == 0
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
