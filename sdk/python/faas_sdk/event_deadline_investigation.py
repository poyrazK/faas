"""One read-only current triage page for a retained Python deadline alert."""

from __future__ import annotations

import asyncio
import json
from dataclasses import dataclass
from typing import Any, NoReturn, cast
from uuid import UUID

from ._event_deadline_policy import EVENT_DEADLINE_REASONS, count, duration, timestamp, uuid
from .api.events import list_event_subscription_deadlines
from .client import AuthenticatedClient
from .event_deadline_checkpoint import validate_event_deadline_checkpoint


@dataclass(frozen=True)
class EventDeadlineInvestigationOptions:
    """One page; retain limit and selected alert when continuing with an opaque cursor."""

    limit: int = 50
    cursor: str = ""
    timeout_ms: int = 5000


class EventDeadlineInvestigationReadError(RuntimeError):
    """The triage endpoint returned a non-200 status; no partial result is attached."""

    def __init__(self, status_code: int, retry_after: str | None):
        self.status_code = status_code
        self.retry_after = retry_after
        super().__init__(f"deadline investigation read failed: HTTP {status_code}")


_CURSOR_MAX_BYTES = 4096
_MIN_TIME = cast(int, timestamp("0001-01-01T00:00:00Z"))
_MAX_TIME = cast(int, timestamp("9999-12-31T23:59:59.999999999Z"))


def _triage_time(raw: Any) -> int | None:
    at = timestamp(raw)
    return at if at is not None and _MIN_TIME < at <= _MAX_TIME else None


def _valid_cursor(raw: Any) -> bool:
    if not isinstance(raw, str):
        return False
    try:
        return len(raw.encode("utf-8")) <= _CURSOR_MAX_BYTES
    except UnicodeEncodeError:
        return False


def _invalid_page() -> NoReturn:
    raise ValueError("invalid or mismatched deadline investigation page")


async def ainvestigate_event_deadline_alert(
    client: AuthenticatedClient,
    checkpoint: Any,
    reference: dict[str, Any],
    options: EventDeadlineInvestigationOptions | None = None,
) -> dict[str, Any]:
    """Select an exact retained alert, derive its cohort and read one current page.

    Revalidate and detach Python v1/v2 checkpoints on every call. Timeout raises
    TimeoutError and task cancellation propagates; neither returns partial results.
    No checkpoint, notification, delivery or control is changed.
    """
    saved = validate_event_deadline_checkpoint(checkpoint)
    if (
        not isinstance(reference, dict)
        or not isinstance(reference.get("incident_id"), str)
        or not count(reference.get("sequence"))
        or reference["sequence"] == 0
    ):
        raise ValueError("invalid deadline alert reference")
    incident, sequence = reference["incident_id"], reference["sequence"]
    notification_state, alert = "", None
    for state in ("pending", "acknowledged"):
        alert = next((a for a in saved[state] if a["incident_id"] == incident and a["sequence"] == sequence), None)
        if alert is not None:
            notification_state = state
            break
    if alert is None:
        raise ValueError("deadline alert reference is not retained in the checkpoint")
    rule = next(r for r in saved["policy"]["rules"] if r["metric"] == alert["metric"])
    if options is None:
        options = EventDeadlineInvestigationOptions()
    if not isinstance(options, EventDeadlineInvestigationOptions):
        raise ValueError("invalid deadline investigation options")
    limit, cursor, timeout_ms = options.limit, options.cursor, options.timeout_ms
    if not count(limit) or not 1 <= limit <= 100 or not _valid_cursor(cursor) or not duration(timeout_ms, 5000):
        raise ValueError("invalid deadline investigation page size, cursor or timeout")
    query: dict[str, Any] = {"status": rule["cohort"], "limit": limit}
    if rule["cohort"] == "upcoming":
        query["within_millis"] = saved["policy"]["within_ms"]
    if "reason" in rule:
        query["waiting_reason"] = rule["reason"]
    if cursor:
        query["cursor"] = cursor
    target = saved["target"]
    async with asyncio.timeout(timeout_ms / 1000):
        response = await list_event_subscription_deadlines.asyncio_detailed(
            target["app"], UUID(target["subscription_id"]), client=client, **query
        )
        if response.status_code != 200:
            raise EventDeadlineInvestigationReadError(response.status_code, response.headers.get("Retry-After"))
        # Generated model datetimes lose nanoseconds; validate original JSON instead.
        page = _validate_page(json.loads(response.content), target, query)
    return {
        "target": target,
        "alert": alert,
        "notification_state": notification_state,
        "membership": "current_at_triage_observation",
        "read_only": True,
        "query": query,
        "page": page,
    }


def _validate_page(raw: Any, target: dict[str, str], query: dict[str, Any]) -> dict[str, Any]:
    if not isinstance(raw, dict):
        _invalid_page()
    observed, window = _triage_time(raw.get("observed_at")), _triage_time(raw.get("window_at"))
    next_cursor = raw.get("next_cursor", "")
    if (
        raw.get("coverage") != "retained_captured_pending_application_deadlines"
        or raw.get("read_only") is not True
        or not uuid(raw.get("app_id"))
        or raw["app_id"].lower() != target["app_id"]
        or not uuid(raw.get("subscription_id"))
        or raw["subscription_id"].lower() != target["subscription_id"]
        or observed is None
        or window is None
        or window > observed
        or ("cursor" not in query and window != observed)
        or not isinstance(raw.get("recipients"), list)
        or len(raw["recipients"]) > query["limit"]
        or type(raw.get("has_more")) is not bool
        or not _valid_cursor(next_cursor)
        or raw["has_more"] != bool(next_cursor)
        or (raw["has_more"] and len(raw["recipients"]) != query["limit"])
        or ("cursor" in query and next_cursor == query["cursor"])
    ):
        _invalid_page()
    recipients, previous = [], None
    for row in raw["recipients"]:
        recipient, key = _validate_recipient(row, window, observed, query)
        if previous is not None and key < previous:
            _invalid_page()
        recipients.append(recipient)
        previous = key
    # Reconstruct known fields, dropping unknown response metadata and payloads.
    return {
        "coverage": raw["coverage"],
        "app_id": target["app_id"],
        "subscription_id": target["subscription_id"],
        "observed_at": raw["observed_at"],
        "window_at": raw["window_at"],
        "read_only": True,
        "recipients": recipients,
        "has_more": raw["has_more"],
        **({"next_cursor": next_cursor} if next_cursor else {}),
    }


def _validate_recipient(
    raw: Any, window: int, observed: int, query: dict[str, Any]
) -> tuple[dict[str, Any], tuple[int, int]]:
    if not isinstance(raw, dict):
        _invalid_page()
    accepted, deadline = _triage_time(raw.get("accepted_at")), _triage_time(raw.get("deadline_at"))
    if (
        not isinstance(raw.get("event_source"), str)
        or not raw["event_source"]
        or not isinstance(raw.get("event_id"), str)
        or not raw["event_id"]
        or not isinstance(raw.get("waiting_reason"), str)
        or raw["waiting_reason"] not in EVENT_DEADLINE_REASONS
        or accepted is None
        or deadline is None
        or accepted > window
        or deadline <= accepted
        or type(raw.get("expired")) is not bool
        or raw["expired"] != (deadline <= observed)
        or ("waiting_reason" in query and raw["waiting_reason"] != query["waiting_reason"])
        or (query["status"] == "expired" and deadline > window)
        or (query["status"] == "upcoming" and not window < deadline < window + query["within_millis"] * 1000000)
        or any(k in raw and _triage_time(raw[k]) is None for k in ("next_attempt_at", "lease_until"))
    ):
        _invalid_page()
    fields = ("event_source", "event_id", "accepted_at", "deadline_at", "expired", "waiting_reason")
    recipient = {k: raw[k] for k in fields}
    recipient.update({k: raw[k] for k in ("next_attempt_at", "lease_until") if k in raw})
    return recipient, (deadline, accepted)
