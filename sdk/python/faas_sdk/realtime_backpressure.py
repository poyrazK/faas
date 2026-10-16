"""Bounded, payload-free observations of a retained WebSocket event queue."""

from __future__ import annotations

import asyncio
import math
import time
from collections import deque
from collections.abc import Callable
from dataclasses import dataclass
from typing import Literal


@dataclass(frozen=True, kw_only=True)
class RealtimeBackpressureStatus:
    state: Literal["inactive", "healthy", "busy", "overloaded"]
    active: bool
    queue_depth: int
    capacity: int
    oldest_queued_age_seconds: float | None
    overflow_count: int


@dataclass(frozen=True, kw_only=True)
class RealtimeProcessingStatus:
    """Local handler timing, independent of transport and server sequence lag."""

    processing: bool
    active_handler_age_seconds: float | None
    last_handler_duration_seconds: float | None


class RealtimeBackpressureMonitor:
    """One subscription's queue, excluding the handler's in-flight event.

    Age uses a monotonic clock. Callbacks run synchronously on the owning event
    loop at queue changes and age thresholds; keep them fast. Observations never
    pause delivery, change ACKs or alter the transport's overflow policy.
    """

    def __init__(
        self,
        *,
        on_change: Callable[[RealtimeBackpressureStatus], None] | None = None,
        busy_ratio: float = 0.5,
        overloaded_ratio: float = 0.9,
        busy_age_seconds: float = 5,
        overloaded_age_seconds: float = 30,
    ):
        if on_change is not None and not callable(on_change):
            raise ValueError("Invalid backpressure callback")
        for value in (busy_ratio, overloaded_ratio, busy_age_seconds, overloaded_age_seconds):
            if type(value) not in (int, float) or not math.isfinite(value):
                raise ValueError("Backpressure thresholds must be finite numbers")
        if not 0 < busy_ratio < overloaded_ratio <= 1 or not 0.1 <= busy_age_seconds < overloaded_age_seconds <= 86400:
            raise ValueError("Invalid backpressure thresholds")
        self._callback = on_change
        self._busy_ratio, self._overloaded_ratio = busy_ratio, overloaded_ratio
        self._busy_age, self._overloaded_age = busy_age_seconds, overloaded_age_seconds
        self._owner: object | None = None
        self._capacity = 0
        self._times: deque[float] = deque()
        self._overflows = 0
        self._overflowed = False
        self._timer: asyncio.TimerHandle | None = None

    def snapshot(self) -> RealtimeBackpressureStatus:
        age = max(0, time.monotonic() - self._times[0]) if self._times else None
        depth = len(self._times)
        active = self._owner is not None
        state: Literal["inactive", "healthy", "busy", "overloaded"] = "inactive"
        if active:
            state = "healthy"
            ratio = depth / self._capacity
            if ratio >= self._busy_ratio or age is not None and age >= self._busy_age:
                state = "busy"
            if self._overflowed or ratio >= self._overloaded_ratio or age is not None and age >= self._overloaded_age:
                state = "overloaded"
        return RealtimeBackpressureStatus(
            state=state,
            active=active,
            queue_depth=depth,
            capacity=self._capacity,
            oldest_queued_age_seconds=age,
            overflow_count=self._overflows,
        )

    def _bind(self, owner: object, capacity: int) -> None:
        if self._owner is owner:
            return
        if self._owner is not None:
            raise ValueError("Backpressure monitor already belongs to a session")
        if type(capacity) is not int or not 1 <= capacity <= 8192:
            raise ValueError("Invalid queue capacity")
        self._owner, self._capacity, self._overflowed = owner, capacity, False
        self._times.clear()
        self._publish()

    def _publish(self) -> None:
        if self._timer is not None:
            self._timer.cancel()
            self._timer = None
        status = self.snapshot()
        if self._callback is not None and self._owner is not None and self._times:
            age = status.oldest_queued_age_seconds
            assert age is not None
            future = [threshold - age for threshold in (self._busy_age, self._overloaded_age) if threshold > age]
            if future:
                self._timer = asyncio.get_running_loop().call_later(min(future), self._publish)
        if self._callback is not None:
            try:
                self._callback(status)
            except Exception:
                pass

    def _append(self, owner: object, *, front_time: float | None = None) -> None:
        if self._owner is not owner:
            return
        if front_time is None:
            self._times.append(time.monotonic())
        else:
            self._times.appendleft(front_time)
        self._publish()

    def _pop(self, owner: object) -> float:
        if self._owner is not owner or not self._times:
            raise ValueError("Backpressure queue ownership mismatch")
        timestamp = self._times.popleft()
        self._publish()
        return timestamp

    def _overflow(self, owner: object) -> None:
        if self._owner is owner:
            self._overflows = min(2**63 - 1, self._overflows + 1)
            self._overflowed = True
            self._publish()

    def _invalidate(self, owner: object) -> None:
        if self._owner is owner:
            self._owner = None
            self._times.clear()
            self._publish()
