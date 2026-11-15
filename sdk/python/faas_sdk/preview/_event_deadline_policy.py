"""Deadline policy normalization and complete raw-response evidence validation."""

from __future__ import annotations

import re
from dataclasses import dataclass, field
from datetime import datetime
from typing import Any

MAX_COUNT = 9007199254740991
QUEUE_MAX = 1024
EVENT_DEADLINE_RATIO_BASIS_POINTS_MAX = 10000
EVENT_DEADLINE_REASONS = (
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
)


@dataclass(frozen=True)
class EventDeadlineTarget:
    app: str
    app_id: str
    subscription_id: str


@dataclass(frozen=True)
class EventDeadlineRatioOptions:
    warning_basis_points: int
    minimum_recipients: int
    recovery_basis_points: int | None = None


@dataclass(frozen=True)
class EventDeadlinePolicyOptions:
    within_ms: int
    upcoming: int = 0
    expired: int = 0
    upcoming_recovery: int | None = None
    expired_recovery: int | None = None
    upcoming_reasons: dict[str, int] = field(default_factory=dict)
    expired_reasons: dict[str, int] = field(default_factory=dict)
    upcoming_reasons_recovery: dict[str, int] = field(default_factory=dict)
    expired_reasons_recovery: dict[str, int] = field(default_factory=dict)
    sustain_ms: int = 60000
    recovery_sustain_ms: int = 60000
    expired_ratio: EventDeadlineRatioOptions | None = field(default=None, kw_only=True)


def count(value: Any) -> bool:
    return type(value) is int and 0 <= value <= MAX_COUNT


def duration(value: Any, maximum: int = 86400000) -> bool:
    return type(value) is int and 100 <= value <= maximum


def uuid(value: Any) -> bool:
    return (
        isinstance(value, str)
        and re.fullmatch(r"[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}", value) is not None
        and value != "00000000-0000-0000-0000-000000000000"
    )


def target_dict(target: EventDeadlineTarget) -> dict[str, str]:
    if (
        not isinstance(target, EventDeadlineTarget)
        or not isinstance(target.app, str)
        or not target.app.strip()
        or len(target.app) > 253
        or not uuid(target.app_id)
        or not uuid(target.subscription_id)
    ):
        raise ValueError("invalid deadline watch target")
    return {"app": target.app, "app_id": target.app_id.lower(), "subscription_id": target.subscription_id.lower()}


def policy_dict(options: EventDeadlinePolicyOptions) -> dict[str, Any]:
    if (
        not isinstance(options, EventDeadlinePolicyOptions)
        or not duration(options.within_ms)
        or not duration(options.sustain_ms)
        or not duration(options.recovery_sustain_ms)
    ):
        raise ValueError("deadline horizon and confirmation durations require integer milliseconds 100..86400000")
    rules: list[dict[str, Any]] = []

    def add(cohort: str, warning: int, recovery: int | None, reason: str | None = None) -> None:
        if not count(warning) or (recovery is not None and (not count(recovery) or recovery >= warning)):
            raise ValueError("deadline recovery requires 0 <= recovery < enabled warning, within safe integer bounds")
        if warning:
            rule = {
                "metric": f"deadline_window.{cohort}_recipients.{options.within_ms}",
                "cohort": cohort,
                "threshold": warning,
                "recovery_threshold": warning - 1 if recovery is None else recovery,
            }
            if reason is not None:
                rule.update(reason=reason, metric=rule["metric"] + ".reason." + reason)
            rules.append(rule)

    add("upcoming", options.upcoming, options.upcoming_recovery)
    add("expired", options.expired, options.expired_recovery)
    for cohort, warnings, recoveries in (
        ("upcoming", options.upcoming_reasons, options.upcoming_reasons_recovery),
        ("expired", options.expired_reasons, options.expired_reasons_recovery),
    ):
        for mapping in (warnings, recoveries):
            if not isinstance(mapping, dict) or any(
                k not in EVENT_DEADLINE_REASONS or not count(v) for k, v in mapping.items()
            ):
                raise ValueError("deadline reason counts require known labels and safe nonnegative integers")
        for reason in sorted(warnings.keys() | recoveries.keys()):
            add(cohort, warnings.get(reason, 0), recoveries.get(reason), reason)
    if options.expired_ratio is not None:
        rules.append(ratio_rule(options.expired_ratio, options.within_ms))
    if not rules:
        raise ValueError("enable at least one deadline count or expired ratio warning")
    return {
        "within_ms": options.within_ms,
        "sustain_ms": options.sustain_ms,
        "recovery_sustain_ms": options.recovery_sustain_ms,
        "rules": rules,
    }


def ratio_rule(options: EventDeadlineRatioOptions, within: int) -> dict[str, Any]:
    if (
        not isinstance(options, EventDeadlineRatioOptions)
        or not count(options.warning_basis_points)
        or not 1 <= options.warning_basis_points <= EVENT_DEADLINE_RATIO_BASIS_POINTS_MAX
        or not count(options.minimum_recipients)
        or options.minimum_recipients < 1
        or (
            options.recovery_basis_points is not None
            and (
                not count(options.recovery_basis_points)
                or options.recovery_basis_points >= options.warning_basis_points
            )
        )
    ):
        raise ValueError(
            "ratio requires warning basis points 1..10000, lower nonnegative recovery and a positive safe-integer minimum cohort"
        )
    return {
        "metric": f"deadline_window.expired_ratio_basis_points.{within}.minimum_recipients.{options.minimum_recipients}",
        "cohort": "expired",
        "threshold": options.warning_basis_points,
        "recovery_threshold": options.warning_basis_points - 1
        if options.recovery_basis_points is None
        else options.recovery_basis_points,
        "minimum_recipients": options.minimum_recipients,
    }


def valid_ratio_sample(rule: dict[str, Any], sample: Any) -> bool:
    return (
        isinstance(sample, dict)
        and count(rule.get("minimum_recipients"))
        and rule["minimum_recipients"] > 0
        and count(sample.get("expired_recipients"))
        and count(sample.get("deadline_recipients"))
        and sample["deadline_recipients"] >= rule["minimum_recipients"]
        and sample["expired_recipients"] <= sample["deadline_recipients"]
    )


def ratio_value(sample: dict[str, int]) -> float:
    """Approximate basis points for display; decisions always use integer products."""
    whole, remainder = divmod(
        sample["expired_recipients"] * EVENT_DEADLINE_RATIO_BASIS_POINTS_MAX, sample["deadline_recipients"]
    )
    return whole + remainder / sample["deadline_recipients"]


def valid_deadline_value(rule: dict[str, Any], value: Any, sample: Any) -> bool:
    if "minimum_recipients" not in rule:
        return count(value) and sample is None
    return (
        valid_ratio_sample(rule, sample)
        and type(value) in (int, float)
        and 0 <= value <= EVENT_DEADLINE_RATIO_BASIS_POINTS_MAX
        and value == ratio_value(sample)
    )


def deadline_boundaries(condition: dict[str, Any]) -> tuple[bool, bool]:
    if "value" not in condition:
        return False, False
    if "minimum_recipients" not in condition:
        return condition["value"] >= condition["threshold"], condition["value"] <= condition["recovery_threshold"]
    sample = condition.get("ratio_sample")
    if not valid_ratio_sample(condition, sample):
        return False, False
    scaled = sample["expired_recipients"] * EVENT_DEADLINE_RATIO_BASIS_POINTS_MAX
    total = sample["deadline_recipients"]
    return scaled >= condition["threshold"] * total, scaled <= condition["recovery_threshold"] * total


def timestamp(raw: Any) -> int | None:
    """RFC3339 nanoseconds, preserving ordering beyond datetime microseconds."""
    if not isinstance(raw, str):
        return None
    match = re.fullmatch(r"(\d{4}-\d{2}-\d{2})T(\d{2}:\d{2}:\d{2})(?:\.(\d{1,9}))?(Z|([+-])(\d{2}):(\d{2}))", raw)
    if not match:
        return None
    try:
        at = datetime.fromisoformat(match[1] + "T" + match[2])
        hours, minutes = int(match[6] or 0), int(match[7] or 0)
        if hours > 23 or minutes > 59:
            return None
        delta = at - datetime(1970, 1, 1)
        offset = (hours * 60 + minutes) * 60 * (-1 if match[5] == "-" else 1)
        return ((delta.days * 86400 + delta.seconds) - offset) * 1000000000 + int((match[3] or "").ljust(9, "0"))
    except (ValueError, OverflowError):
        return None


def partition(value: Any, total: int) -> bool:
    if not isinstance(value, dict) or set(value) != set(EVENT_DEADLINE_REASONS):
        return False
    for key in EVENT_DEADLINE_REASONS:
        n = value[key]
        if not count(n) or n > total:
            return False
        total -= n
    return total == 0


def deadline_evidence(value: Any, target: dict[str, str], within: int) -> bool:
    if not isinstance(value, dict) or not isinstance(value.get("summary"), dict):
        return False
    s = value["summary"]
    at, end = timestamp(s.get("observed_at")), timestamp(value.get("window_end_at"))
    if (
        value.get("coverage") != "all_retained_captured_pending_application_deadline_window"
        or s.get("coverage") != "all_retained_captured_pending_application_recipients"
        or not uuid(s.get("app_id"))
        or not uuid(s.get("subscription_id"))
        or s["app_id"].lower() != target["app_id"]
        or s["subscription_id"].lower() != target["subscription_id"]
        or s.get("read_only") is not True
        or s.get("complete") is not True
        or at is None
        or end is None
        or type(value.get("within_millis")) is not int
        or value["within_millis"] != within
        or end != at + within * 1000000
        or not count(s.get("pending_recipients"))
        or not count(s.get("deadline_recipients"))
        or s["deadline_recipients"] > s["pending_recipients"]
        or not count(s.get("already_expired_count"))
        or s["already_expired_count"] > s["deadline_recipients"]
        or not count(value.get("upcoming_recipients"))
        or value["upcoming_recipients"] > s["deadline_recipients"] - s["already_expired_count"]
        or not partition(value.get("upcoming_waiting_reasons"), value["upcoming_recipients"])
        or not partition(value.get("expired_waiting_reasons"), s["already_expired_count"])
    ):
        return False
    earliest, upcoming = timestamp(s.get("earliest_deadline_at")), timestamp(value.get("earliest_upcoming_deadline_at"))
    if (
        (s["deadline_recipients"] == 0) != ("earliest_deadline_at" not in s)
        or (s["deadline_recipients"] > 0 and (earliest is None or (earliest <= at) != (s["already_expired_count"] > 0)))
        or (value["upcoming_recipients"] == 0) != ("earliest_upcoming_deadline_at" not in value)
        or (value["upcoming_recipients"] > 0 and (upcoming is None or not at < upcoming < end))
    ):
        return False
    if earliest is not None and earliest > at:
        if (earliest < end and upcoming != earliest) or (earliest >= end and value["upcoming_recipients"] != 0):
            return False
    return controls(s, at)


def controls(s: dict[str, Any], at: int) -> bool:
    c = s.get("current")
    if (
        not isinstance(c, dict)
        or timestamp(c.get("observed_at")) != at
        or type(c.get("enabled")) is not bool
        or type(c.get("rate_limited")) is not bool
        or any(not count(c.get(k)) for k in ("revision", "interval_millis", "burst", "available_tokens"))
        or type(s.get("paused")) is not bool
        or not count(s.get("legacy_rate_per_second"))
        or s["legacy_rate_per_second"] > 100
        or s.get("circuit_state") not in ("closed", "open", "half_open", "draining")
    ):
        return False
    if (
        not 10 <= c["interval_millis"] <= 3600000
        or not 1 <= c["burst"] <= 100
        or c["available_tokens"] > c["burst"]
        or c["rate_limited"] != (c["enabled"] and c["available_tokens"] == 0)
        or (not c["enabled"] and c["available_tokens"] != 0)
    ):
        return False
    if c["rate_limited"]:
        next_at = timestamp(c.get("next_available_at"))
        if next_at is None or not at < next_at <= at + c["interval_millis"] * 1000000:
            return False
    elif "next_available_at" in c:
        return False
    scopes, constraints = s.get("capacity_blocking_scopes"), s.get("constraints")
    known = (
        "subscription_paused",
        "legacy_rate_limit",
        "circuit_open",
        "circuit_half_open",
        "circuit_draining",
        "capacity_consumer",
        "capacity_app",
        "capacity_account",
        "active_leases",
        "scheduled_retries",
    )
    return (
        isinstance(scopes, list)
        and all(isinstance(v, str) and v in ("consumer", "app", "account") for v in scopes)
        and len(set(scopes)) == len(scopes)
        and isinstance(constraints, list)
        and all(isinstance(v, str) and v in known for v in constraints)
        and len(set(constraints)) == len(constraints)
    )
