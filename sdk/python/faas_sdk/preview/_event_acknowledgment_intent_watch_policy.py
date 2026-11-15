"""Bounded recovery watch policy and confirmation state, aligned with Go/Node."""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Literal

from ._event_deadline_policy import timestamp
from .event_acknowledgment_intent_health import (
    EventAcknowledgmentIntentHealth,
    EventAcknowledgmentIntentHealthOptions,
)
from .event_acknowledgment_intent_health import (
    _options as health_options,
)

Metric = Literal["due_backlog", "stalled_recovery", "parked_intents"]
Change = Literal["unknown", "confirmation_lost", "firing", "resolved"]
Loss = Literal["read_failed", "incomplete", "stale", "out_of_order", "sample_gap"]
Reason = Literal[
    "read_failed", "incomplete", "stale", "out_of_order", "sample_gap", "threshold_exceeded", "threshold_cleared"
]


@dataclass(frozen=True)
class EventAcknowledgmentIntentWatchOptions:
    health: EventAcknowledgmentIntentHealthOptions | None = None
    due_threshold: int = 0
    stalled_threshold: int = 0
    poll_interval_ms: int = 5000
    query_timeout_ms: int = 5000
    max_age_ms: int | None = None
    sustain_ms: int | None = None
    # Keyword-only preserves positional options, including inherited checkpoint callbacks.
    parked_threshold: int = field(default=0, kw_only=True)


@dataclass(frozen=True)
class EventAcknowledgmentIntentAlert:
    """Metadata only. Zero value without health does not establish an empty backlog."""

    account_id: str
    app_id: str
    metric: Metric
    change: Change
    reason: Reason
    at: str
    value: int
    threshold: int
    health: EventAcknowledgmentIntentHealth | None = None


class EventAcknowledgmentIntentWatchCallbackError(RuntimeError):
    """Notification outcome unconfirmed; exposes the frozen alert without an arbitrary cause."""

    code = "event_acknowledgment_intent_watch_callback_failed"

    def __init__(self, alert: EventAcknowledgmentIntentAlert):
        super().__init__("acknowledgment intent watch callback failed; local notification is unconfirmed")
        self.alert = alert


@dataclass(frozen=True)
class Policy:
    health: EventAcknowledgmentIntentHealthOptions
    due_threshold: int
    stalled_threshold: int
    poll_interval_ms: int
    query_timeout_ms: int
    max_age_ms: int
    sustain_ms: int
    parked_threshold: int = 0


def _integer(value: int, minimum: int, maximum: int) -> bool:
    return type(value) is int and minimum <= value <= maximum


def policy(options: EventAcknowledgmentIntentWatchOptions) -> Policy:
    if not isinstance(options, EventAcknowledgmentIntentWatchOptions):
        raise ValueError("invalid acknowledgment intent watch policy")
    limit, stall = health_options(options.health)
    due, stalled, parked = options.due_threshold, options.stalled_threshold, options.parked_threshold
    poll, query = options.poll_interval_ms, options.query_timeout_ms
    if (
        not _integer(due, 0, limit)
        or not _integer(stalled, 0, limit)
        or not _integer(parked, 0, limit)
        or due == stalled == parked == 0
        or not _integer(poll, 100, 60000)
        or not _integer(query, 10, 30000)
    ):
        raise ValueError("invalid acknowledgment intent watch policy")
    age = max(3 * poll, 2 * query) if options.max_age_ms is None else options.max_age_ms
    sustain = max(30000, poll) if options.sustain_ms is None else options.sustain_ms
    if not _integer(age, max(2 * poll, query), 300000) or not _integer(sustain, poll, 86400000):
        raise ValueError("invalid acknowledgment intent watch policy")
    return Policy(EventAcknowledgmentIntentHealthOptions(limit, stall), due, stalled, poll, query, age, sustain, parked)


def evidence(
    health: EventAcknowledgmentIntentHealth | None,
    previous_observed: int | None,
    previous_poll: int | None,
    wall: int,
    local: int,
    max_age: int,
) -> Loss | None:
    """Wall/database instants and monotonic durations use integer nanoseconds."""
    if health is None:
        return "read_failed"
    if not health.complete or health.truncated or health.invalid_rows != 0:
        return "incomplete"
    observed = timestamp(health.observed_at)
    if observed is None or observed > wall or wall - observed > max_age:
        return "stale"
    if previous_observed is not None and observed <= previous_observed:
        return "out_of_order"
    if previous_poll is not None and local - previous_poll > max_age:
        return "sample_gap"
    return None


@dataclass
class Condition:
    metric: Metric
    threshold: int
    state: str = "unknown"
    reason: Loss | None = None
    first: int | None = None
    samples: int = 0
    clearing: bool = False

    def reset(self) -> None:
        self.first, self.samples, self.clearing = None, 0, False

    def update(self, value: int, reason: Loss | None, local: int, sustain: int) -> tuple[Change, Reason] | None:
        if reason is not None:
            change: Change = "confirmation_lost" if self.state == "firing" else "unknown"
            emit = self.state != "unknown" or self.reason != reason
            self.state, self.reason = "unknown", reason
            self.reset()
            return (change, reason) if emit else None
        self.reason = None
        breach = value >= self.threshold
        if self.state == "firing":
            if breach:
                self.reset()
                return None
            if not self.clearing:
                self.first, self.samples, self.clearing = local, 1, True
                return None
            self.samples += 1
            if self.samples >= 2 and self.first is not None and local - self.first >= sustain:
                self.state = "clear"
                self.reset()
                return "resolved", "threshold_cleared"
            return None
        if not breach:
            self.state = "clear"
            self.reset()
            return None
        if self.state != "pending":
            self.state, self.first, self.samples = "pending", local, 1
            return None
        self.samples += 1
        if self.samples >= 2 and self.first is not None and local - self.first >= sustain:
            self.state = "firing"
            self.reset()
            return "firing", "threshold_exceeded"
        return None
