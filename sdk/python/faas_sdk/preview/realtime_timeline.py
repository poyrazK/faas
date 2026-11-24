"""Bounded local transition history; never stores payloads or transport error text."""

from __future__ import annotations

import time
from collections import OrderedDict, deque
from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Literal

from .realtime_catch_up import RealtimeCatchUpStatus
from .realtime_connection_status import RealtimeConnectionStatus
from .realtime_subscriptions import RealtimeSubscriptionStatus, RealtimeSubscriptionSummary, _identity

_TIMELINE_STATES = {
    "connection": {"offline", "connecting", "connected", "reconnecting"},
    "subscription": {"pending", "subscribed", "denied", "resync_required"},
    "catch_up": {"idle", "waiting_target", "catching_up", "caught_up", "resync_required", "stalled"},
}


@dataclass(frozen=True, kw_only=True)
class RealtimeIncidentEvent:
    event_id: int
    occurred_at: datetime
    elapsed_seconds: float
    category: Literal["connection", "subscription", "catch_up"]
    state: str
    attempt_id: int
    generation: int | None = None
    channel: str | None = None
    inbox: bool = False
    acknowledgement_missing: bool = False


def _number(value: int) -> None:
    if type(value) is not int or not 0 <= value <= 2**63 - 1:
        raise ValueError("Invalid timeline attempt/generation")


class RealtimeIncidentTimeline:
    """Record from existing tracker callbacks on one execution context.

    Snapshot contains channel identifiers; diagnostics export redacts them by
    default. Both retained events and transition-deduplication memory are bounded.
    """

    def __init__(self, *, capacity: int = 256, max_identities: int = 1024):
        for name, value in (("capacity", capacity), ("max_identities", max_identities)):
            if type(value) is not int or not 1 <= value <= 8192:
                raise ValueError(f"{name} must be an integer between 1 and 8192")
        self._events: deque[RealtimeIncidentEvent] = deque(maxlen=capacity)
        self._markers: OrderedDict[tuple, tuple] = OrderedDict()
        self._max_identities = max_identities
        self._started = time.monotonic()
        self._next_id = 0
        self._evicted = 0

    def _record(
        self,
        category: str,
        state: str,
        attempt: int,
        generation: int | None = None,
        channel: str | None = None,
        inbox: bool = False,
        missing: bool = False,
    ) -> bool:
        if state not in _TIMELINE_STATES[category]:
            raise ValueError("Invalid timeline transition state")
        _number(attempt)
        if generation is not None:
            _number(generation)
        if type(missing) is not bool:
            raise ValueError("Invalid acknowledgement flag")
        if category != "connection":
            _identity(channel, inbox)
        key = category, inbox, channel
        marker = state, attempt, generation, missing
        previous = self._markers.get(key)
        self._markers[key] = marker
        self._markers.move_to_end(key)
        if len(self._markers) > self._max_identities:
            self._markers.popitem(last=False)
        if previous == marker:
            return False
        self._next_id += 1
        if len(self._events) == self._events.maxlen:
            self._evicted += 1
        self._events.append(
            RealtimeIncidentEvent(
                event_id=self._next_id,
                occurred_at=datetime.now(timezone.utc),
                elapsed_seconds=max(0.0, time.monotonic() - self._started),
                category=category,
                state=state,
                attempt_id=attempt,
                generation=generation,
                channel=channel,
                inbox=inbox,
                acknowledgement_missing=missing,
            )
        )
        return True

    def record_connection(self, status: RealtimeConnectionStatus) -> bool:
        if not isinstance(status, RealtimeConnectionStatus):
            raise ValueError("Expected connection status")
        return self._record("connection", status.state, status.attempt_id)

    def record_subscriptions(self, summary: RealtimeSubscriptionSummary) -> int:
        if not isinstance(summary, RealtimeSubscriptionSummary) or len(summary.subscriptions) > 8192:
            raise ValueError("Expected bounded subscription summary")
        _number(summary.attempt_id)
        _number(summary.generation)
        seen = set()
        # Validate the entire batch before recording any entry.
        for entry in summary.subscriptions:
            if not isinstance(entry, RealtimeSubscriptionStatus):
                raise ValueError("Invalid subscription timeline entry")
            key = _identity(entry.channel, entry.inbox)
            if (
                key in seen
                or entry.state not in _TIMELINE_STATES["subscription"]
                or type(entry.acknowledgement_missing) is not bool
            ):
                raise ValueError("Invalid subscription timeline entries")
            seen.add(key)
        return sum(
            self._record(
                "subscription",
                entry.state,
                summary.attempt_id,
                summary.generation,
                entry.channel,
                entry.inbox,
                entry.acknowledgement_missing,
            )
            for entry in summary.subscriptions
        )

    def record_catch_up(self, status: RealtimeCatchUpStatus) -> bool:
        if not isinstance(status, RealtimeCatchUpStatus):
            raise ValueError("Expected catch-up status")
        return self._record(
            "catch_up", status.state, status.attempt_id, status.generation, status.channel, status.inbox
        )

    def snapshot(self) -> tuple[RealtimeIncidentEvent, ...]:
        return tuple(self._events)

    @property
    def evicted_count(self) -> int:
        return self._evicted

    def clear(self) -> None:
        """Clear events, ordering markers and counters; start a new local timeline."""
        self._events.clear()
        self._markers.clear()
        self._next_id = self._evicted = 0
        self._started = time.monotonic()
