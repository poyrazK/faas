"""Validated Python recovery-watch checkpoints and exact lifecycle acknowledgments."""

from __future__ import annotations

import re
from dataclasses import asdict
from typing import Any, NoReturn

from ._event_acknowledgment_intent_watch_policy import EventAcknowledgmentIntentWatchOptions, Policy, policy
from ._event_deadline_policy import timestamp
from .event_acknowledgment_intent_health import EventAcknowledgmentIntentHealthOptions
from .event_consumer_transactions import _uuid

EVENT_ACKNOWLEDGMENT_INTENT_OUTBOX_MAX = 1024
KIND = "gregale.python.event-acknowledgment-intent-watch"
MAX_SEQUENCE = 9007199254740991


def _invalid() -> NoReturn:
    raise ValueError("invalid Python acknowledgment intent watch checkpoint")


def _count(value: Any, maximum: int = MAX_SEQUENCE) -> bool:
    return type(value) is int and 0 <= value <= maximum


def checkpoint_time(value: Any) -> int:
    if (
        not isinstance(value, str)
        or re.fullmatch(r"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}\.[0-9]{6}Z", value) is None
    ):
        _invalid()
    at = timestamp(value)
    if at is None:
        _invalid()
    return at


def _opening(value: Any, session: str) -> int:
    if not isinstance(value, str) or len(value) > 53 or not value.startswith(session + "-"):
        _invalid()
    raw = value[len(session) + 1 :]
    if re.fullmatch(r"[1-9][0-9]{0,15}", raw) is None or not _count(int(raw)):
        _invalid()
    return int(raw)


def rules(settings: Policy) -> list[dict[str, Any]]:
    return [
        {"metric": metric, "threshold": threshold}
        for metric, threshold in (
            ("due_backlog", settings.due_threshold),
            ("stalled_recovery", settings.stalled_threshold),
        )
        if threshold > 0
    ]


def _checkpoint_policy(value: Any) -> Policy:
    if not isinstance(value, dict) or not isinstance(value.get("health"), dict):
        _invalid()
    health = value["health"]
    fields = ("due_threshold", "stalled_threshold", "poll_interval_ms", "query_timeout_ms", "max_age_ms", "sustain_ms")
    if any(type(value.get(key)) is not int for key in fields) or any(
        type(health.get(key)) is not int for key in ("scan_limit", "stall_after_ms")
    ):
        _invalid()
    return policy(
        EventAcknowledgmentIntentWatchOptions(
            health=EventAcknowledgmentIntentHealthOptions(health["scan_limit"], health["stall_after_ms"]),
            **{key: value[key] for key in fields},
        )
    )


def validate_event_acknowledgment_intent_checkpoint(value: Any) -> dict[str, Any]:
    """Reconstruct bounded known fields. Historical values/timestamps are not live proof."""
    failed = False
    try:
        out = _validate(value)
    except Exception:
        failed = True
    if failed:
        _invalid()
    return out


def _validate(value: Any) -> dict[str, Any]:
    if (
        not isinstance(value, dict)
        or value.get("kind") != KIND
        or type(value.get("version")) is not int
        or value["version"] != 1
        or not isinstance(value.get("target"), dict)
        or not _count(value.get("sequence"))
        or not isinstance(value.get("session"), str)
        or _uuid(value["session"]) != value["session"]
    ):
        _invalid()
    target = {key: _uuid(value["target"].get(key)) for key in ("account_id", "app_id")}
    settings = _checkpoint_policy(value.get("policy"))
    expected = rules(settings)
    session, sequence = value["session"], value["sequence"]
    last_observed = None if "last_observed_at" not in value else checkpoint_time(value["last_observed_at"])
    if sequence > 0 and last_observed is None:
        _invalid()
    conditions, pending, acknowledged = (value.get(key) for key in ("conditions", "pending", "acknowledged"))
    if (
        not isinstance(conditions, list)
        or len(conditions) != len(expected)
        or not isinstance(pending, list)
        or len(pending) > EVENT_ACKNOWLEDGMENT_INTENT_OUTBOX_MAX
        or not isinstance(acknowledged, list)
        or len(acknowledged) > EVENT_ACKNOWLEDGMENT_INTENT_OUTBOX_MAX
    ):
        _invalid()
    active: dict[str, str] = {}
    normalized_conditions = []
    for condition, rule in zip(conditions, expected, strict=True):
        if (
            not isinstance(condition, dict)
            or condition.get("metric") != rule["metric"]
            or type(condition.get("threshold")) is not int
            or condition["threshold"] != rule["threshold"]
            or condition.get("state") not in ("unknown", "clear", "firing")
            or ("value" in condition) != ("observed_at" in condition)
        ):
            _invalid()
        if "value" in condition and (
            not _count(condition["value"], settings.health.scan_limit)
            or last_observed is None
            or checkpoint_time(condition["observed_at"]) > last_observed
        ):
            _invalid()
        if "incident_id" in condition:
            incident = condition["incident_id"]
            if (
                _opening(incident, session) > sequence
                or incident in active
                or condition["state"] == "clear"
                or "value" not in condition
            ):
                _invalid()
            active[incident] = rule["metric"]
        elif condition["state"] == "firing":
            _invalid()
        normalized_conditions.append(
            {
                **rule,
                **{key: condition[key] for key in ("state", "value", "observed_at", "incident_id") if key in condition},
            }
        )
    if (
        sequence > 0
        and any("value" not in condition for condition in normalized_conditions)
        or len(pending) + len(active) > EVENT_ACKNOWLEDGMENT_INTENT_OUTBOX_MAX
        or sequence > MAX_SEQUENCE - len(active)
    ):
        _invalid()
    by_metric = {rule["metric"]: rule for rule in expected}
    seen: dict[int, dict[str, Any]] = {}
    lineage: dict[str, dict[str, Any]] = {}

    def notifications(rows: list[Any], is_pending: bool) -> list[dict[str, Any]]:
        last = 0
        out = []
        for notification in rows:
            if (
                not isinstance(notification, dict)
                or not _count(notification.get("sequence"))
                or not last < notification["sequence"] <= sequence
                or notification["sequence"] in seen
                or notification.get("transition") not in ("opened", "resolved")
                or not isinstance(notification.get("metric"), str)
            ):
                _invalid()
            rule = by_metric.get(notification["metric"])
            opening = _opening(notification.get("incident_id"), session)
            checkpoint_time(notification.get("at"))
            if (
                rule is None
                or not _count(notification.get("value"), settings.health.scan_limit)
                or type(notification.get("threshold")) is not int
                or notification["threshold"] != rule["threshold"]
                or opening > notification["sequence"]
                or (
                    notification["transition"] == "opened"
                    and (opening != notification["sequence"] or notification["value"] < rule["threshold"])
                )
                or (
                    notification["transition"] == "resolved"
                    and (opening == notification["sequence"] or notification["value"] >= rule["threshold"])
                )
            ):
                _invalid()
            incident, transition = notification["incident_id"], notification["transition"]
            history = lineage.setdefault(incident, {"metric": rule["metric"]})
            if history["metric"] != rule["metric"] or transition in history:
                _invalid()
            history[transition] = notification["sequence"]
            if transition == "opened":
                history["opening_pending"] = is_pending
            else:
                history["resolution_acked"] = not is_pending
            normalized = {
                key: notification[key]
                for key in ("sequence", "incident_id", "metric", "transition", "at", "value", "threshold")
            }
            seen[notification["sequence"]] = normalized
            last = notification["sequence"]
            out.append(normalized)
        return out

    normalized_pending, normalized_acknowledged = notifications(pending, True), notifications(acknowledged, False)
    for incident, history in lineage.items():
        if (
            "opened" in history
            and "resolved" in history
            and history["resolved"] <= history["opened"]
            or "resolved" in history
            and incident in active
            or "opened" in history
            and "resolved" not in history
            and incident not in active
            or incident in active
            and active[incident] != history["metric"]
            or history.get("opening_pending")
            and history.get("resolution_acked")
        ):
            _invalid()
    for incident in active.keys() | lineage.keys():
        opening_notification = seen.get(_opening(incident, session))
        if opening_notification is not None and (
            opening_notification["incident_id"] != incident or opening_notification["transition"] != "opened"
        ):
            _invalid()
    return {
        "kind": KIND,
        "version": 1,
        "target": target,
        "policy": asdict(settings),
        "session": session,
        "sequence": sequence,
        **({} if last_observed is None else {"last_observed_at": value["last_observed_at"]}),
        "conditions": normalized_conditions,
        "pending": normalized_pending,
        "acknowledged": normalized_acknowledged,
    }


def acknowledge_event_acknowledgment_intent_alerts(
    checkpoint: Any, acknowledgments: list[dict[str, Any]]
) -> dict[str, Any]:
    """All-or-nothing exact pairs; retain the latest 1024 acknowledgment receipts."""
    out = validate_event_acknowledgment_intent_checkpoint(checkpoint)
    if not isinstance(acknowledgments, list) or len(acknowledgments) > EVENT_ACKNOWLEDGMENT_INTENT_OUTBOX_MAX:
        raise ValueError("invalid intent alert acknowledgments")
    known = {
        notification["sequence"]: notification["incident_id"] for notification in out["pending"] + out["acknowledged"]
    }
    keys: set[int] = set()
    for acknowledgment in acknowledgments:
        if (
            not isinstance(acknowledgment, dict)
            or not _count(acknowledgment.get("sequence"))
            or acknowledgment["sequence"] in keys
            or acknowledgment["sequence"] not in known
            or known[acknowledgment["sequence"]] != acknowledgment.get("incident_id")
        ):
            raise ValueError("unknown, duplicate or mismatched intent alert acknowledgment")
        keys.add(acknowledgment["sequence"])
    for notification in out["pending"]:
        if (
            notification["sequence"] in keys
            and notification["transition"] == "resolved"
            and any(
                opening["incident_id"] == notification["incident_id"]
                and opening["transition"] == "opened"
                and opening["sequence"] not in keys
                for opening in out["pending"]
            )
        ):
            raise ValueError("acknowledge opening before or together with resolution")
    out["acknowledged"] = sorted(
        out["acknowledged"] + [notification for notification in out["pending"] if notification["sequence"] in keys],
        key=lambda notification: notification["sequence"],
    )[-EVENT_ACKNOWLEDGMENT_INTENT_OUTBOX_MAX:]
    out["pending"] = [notification for notification in out["pending"] if notification["sequence"] not in keys]
    return out
