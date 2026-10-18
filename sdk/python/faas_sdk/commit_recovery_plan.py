"""Bounded read-only recovery planning for a saved Commit publication selection."""

from __future__ import annotations

import datetime
import re
from typing import Any
from uuid import UUID

from .client import AuthenticatedClient
from .commit_delivery_report import CommitDeliveryReportError, aget_commit_delivery_report


class CommitRecoveryPlanError(Exception):
    """A fresh read failed; plan retains partial evidence and coverage."""

    def __init__(self, plan: dict[str, Any]):
        self.plan = plan
        super().__init__("Commit recovery plan read failed; partial evidence is attached")


def _same_acceptance(left: str, right: str) -> bool:
    pattern = r"(.+?)(?:\.([0-9]{1,9}))?(Z|[+-][0-9]{2}:[0-9]{2})"
    a, b = re.fullmatch(pattern, left), re.fullmatch(pattern, right)
    if not a or not b:
        return False
    try:
        return datetime.datetime.fromisoformat(a[1] + a[3]) == datetime.datetime.fromisoformat(b[1] + b[3]) and (
            a[2] or ""
        ).ljust(9, "0") == (b[2] or "").ljust(9, "0")
    except ValueError:
        return False


def _eligibility(row: dict[str, Any]) -> dict[str, Any]:
    def skip(reason: str) -> dict[str, Any]:
        return {"status": "skipped", "reason": reason}

    if row["status"] == "not_targeted":
        return skip("not_targeted")
    r = row.get("recipient")
    if not r:
        return skip("evidence_unavailable")
    if r.get("workflow_name"):
        return skip("unsupported_recipient")
    execution = (r.get("recovery") or {}).get("latest_replay") or r.get("execution")
    if execution:
        if execution.get("state") == "completed":
            return skip("completed")
        if execution.get("state") in {"pending", "dispatching"}:
            return skip("delivery_active")
    actions = r.get("recovery_actions") or []
    if not actions:
        if r.get("execution_unavailable"):
            return skip("evidence_unavailable")
        if r.get("cancellation"):
            return skip("cancelled")
        return skip("no_recovery_action")
    if len(actions) != 1 or actions[0].get("method") != "POST":
        return skip("unsupported_action")
    action = actions[0]
    kind, url, body = action.get("kind"), action.get("url", ""), action.get("body")
    slug = r.get("app_slug", "")
    valid_slug = bool(re.fullmatch(r"[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]", slug)) and not slug.startswith("tag-")
    uuid = r"[0-9a-fA-F]{8}(-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}"
    if kind == "routing_replay":
        p = row["publication"]
        if (
            not valid_slug
            or url != f"/v1/apps/{slug}/event-deliveries:replay-fanout-failure"
            or not body
            or body.get("allow_expired")
            or body.get("event_source") != p["source"]
            or body.get("event_id") != p["id"]
            or str(body.get("subscription_id", "")).lower() != row["subscription_id"].lower()
        ):
            return skip("action_identity_mismatch")
        generation = body.get("expected_generation")
        if generation is not None and (
            type(generation) is not int
            or generation < 0
            or not body.get("expected_accepted_at")
            or generation != r.get("routing", {}).get("generation")
        ):
            return skip("action_identity_mismatch")
        if body.get("expected_accepted_at") and not _same_acceptance(body["expected_accepted_at"], p["accepted_at"]):
            return skip("action_identity_mismatch")
    elif kind in {"handler_replay", "keyed_handler_replay"}:
        suffix = "/replay" if kind == "handler_replay" else "/replay-keyed"
        invocation = (execution or {}).get("invocation_id", "")
        if not re.fullmatch(uuid, invocation) or url != f"/v1/invocations/{invocation}{suffix}" or body is not None:
            return skip("action_identity_mismatch")
    elif kind == "dead_letter_replay":
        prefix = f"/v1/apps/{slug}/dlq/"
        target = url[len(prefix) : -7] if url.startswith(prefix) and url.endswith("/replay") else ""
        if not valid_slug or not re.fullmatch(uuid, target) or url != f"{prefix}{target}/replay" or body is not None:
            return skip("action_identity_mismatch")
    else:
        return skip("unsupported_action")
    return {"status": "eligible", "action": action}


def _build_plan(report: dict[str, Any]) -> dict[str, Any]:
    plan: dict[str, Any] = {"report": report, "publication_issues": {}, "items": [], "counts": {}}
    for item in report["items"]:
        if item["evidence_status"] != "available":
            plan["publication_issues"][item["acceptance"]["event_id"]] = item["evidence_status"]
        if item["evidence_status"] not in {"available", "partial", "read_error"}:
            continue
        for row in item["subscribers"]:
            result = _eligibility(row)
            plan["items"].append(
                {"event_id": item["acceptance"]["event_id"], "subscription_id": row["subscription_id"], **result}
            )
            status = result["status"]
            plan["counts"][status] = plan["counts"].get(status, 0) + 1
    return plan


async def aplan_commit_recovery(
    client: AuthenticatedClient,
    source: UUID | str,
    saved: dict[str, Any],
    *,
    recipient_pages: int = 1,
    timeout: float = 120,
) -> dict[str, Any]:
    """Refresh saved selection and plan current recovery; never submit actions."""
    try:
        return _build_plan(
            await aget_commit_delivery_report(
                client, source, refresh=saved, recipient_pages=recipient_pages, timeout=timeout
            )
        )
    except CommitDeliveryReportError as error:
        raise CommitRecoveryPlanError(_build_plan(error.report)) from error
