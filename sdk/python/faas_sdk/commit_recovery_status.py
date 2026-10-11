"""Read-only reconciliation of current delivery evidence and saved recovery attempts."""

from __future__ import annotations

import asyncio
import copy
import json
from typing import Any
from uuid import UUID

from .client import AuthenticatedClient
from .commit_delivery_report import CommitDeliveryReportError, aget_commit_delivery_report
from .commit_recovery_plan import _same_acceptance


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
    original = recipient.get("execution")
    latest = (recipient.get("recovery") or {}).get("latest_replay")
    execution = latest or original
    ack = row["attempt"].get("result")
    if row["attempt"]["status"] == "queued" and isinstance(ack, dict) and isinstance(ack.get("id"), str):
        try:
            acknowledged = str(UUID(ack["id"]))
        except ValueError:
            acknowledged = None
        if acknowledged:
            for candidate in (original, latest):
                if candidate and candidate.get("invocation_id", "").lower() == acknowledged:
                    execution = candidate
                    row["attribution"] = "matches_acknowledged_execution"
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
) -> dict[str, Any]:
    """Read guarded evidence only; never resolve an unknown write from delivery advancement."""
    source = str(UUID(str(source)))
    plan, result = copy.deepcopy(reviewed), copy.deepcopy(applied)
    previous = copy.deepcopy(resume)
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
        out["items"] = previous["items"]
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
                    cause = cause or error
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
        status = row["status"]
        out["counts"][status] = out["counts"].get(status, 0) + 1
        if status in {"not_observed", "read_error"}:
            out["remaining"] += 1
    out["complete"] = out["remaining"] == 0
