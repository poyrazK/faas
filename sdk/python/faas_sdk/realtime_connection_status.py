"""Connection lifecycle observation for caller-owned realtime transports."""

from __future__ import annotations

import asyncio
import math
import time
from collections.abc import Callable
from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Literal


@dataclass(frozen=True, kw_only=True)
class RealtimeConnectionStatus:
    state: Literal["connecting", "connected", "reconnecting", "offline"]
    attempt_id: int
    reconnect_attempts: int
    last_connected_at: datetime | None
    views_stale: bool


class RealtimeConnectionStatusTracker:
    """Observe one transport on one execution context; never opens or retries it.

    Call begin_connect for every attempt and retain its ID in transport callbacks.
    on_invalidate should mark read data stale and reset ephemeral presence.
    Callbacks are synchronous and must return quickly.
    """

    def __init__(
        self,
        *,
        on_change: Callable[[RealtimeConnectionStatus], None],
        on_invalidate: Callable[[], None] | None = None,
    ):
        if not callable(on_change) or on_invalidate is not None and not callable(on_invalidate):
            raise ValueError("Connection callbacks must be callable")
        self._on_change = on_change
        self._on_invalidate = on_invalidate
        self._disposed = False
        self._attempted = False
        self._readiness_waiters: set[asyncio.Future[None]] = set()
        self._status = RealtimeConnectionStatus(
            state="offline",
            attempt_id=0,
            reconnect_attempts=0,
            last_connected_at=None,
            views_stale=True,
        )

    @staticmethod
    def _callback(callback: Callable, *args: object) -> None:
        try:
            result = callback(*args)
            if asyncio.iscoroutine(result):
                result.close()
        except (Exception, asyncio.CancelledError):
            pass

    def _change(
        self,
        state: Literal["connecting", "connected", "reconnecting", "offline"],
        *,
        attempt_id: int,
        reconnect_attempts: int,
        last_connected_at: datetime | None,
    ) -> None:
        self._status = RealtimeConnectionStatus(
            state=state,
            attempt_id=attempt_id,
            reconnect_attempts=reconnect_attempts,
            last_connected_at=last_connected_at,
            views_stale=state != "connected",
        )
        status = self._status
        for waiter in tuple(self._readiness_waiters):
            if not waiter.done():
                waiter.set_result(None)
        if state != "connected" and self._on_invalidate is not None:
            self._callback(self._on_invalidate)
        if self._status is status:
            self._callback(self._on_change, status)

    def begin_connect(self) -> int:
        """Observe a new attempt; subsequent attempts are reconnects until success."""
        if self._disposed:
            raise ValueError("Connection status tracker is disposed")
        previous = self._status
        reconnecting = self._attempted
        self._attempted = True
        attempt_id = previous.attempt_id + 1
        self._change(
            "reconnecting" if reconnecting else "connecting",
            attempt_id=attempt_id,
            reconnect_attempts=previous.reconnect_attempts + 1 if reconnecting else 0,
            last_connected_at=previous.last_connected_at,
        )
        return attempt_id

    def _current(self, attempt_id: int) -> bool:
        if type(attempt_id) is not int or attempt_id < 1:
            raise ValueError("attempt_id must be a positive integer")
        return not self._disposed and attempt_id == self._status.attempt_id

    def connected(self, attempt_id: int) -> bool:
        """Observe transport authentication/readiness, ignoring late or duplicate callbacks."""
        if not self._current(attempt_id) or self._status.state not in ("connecting", "reconnecting"):
            return False
        self._change(
            "connected",
            attempt_id=attempt_id,
            reconnect_attempts=0,
            last_connected_at=datetime.now(timezone.utc),
        )
        return True

    def disconnected(self, attempt_id: int) -> bool:
        """Observe failure/closure; counts remain stale until confirmed data is refreshed."""
        if not self._current(attempt_id) or self._status.state == "offline":
            return False
        previous = self._status
        self._change(
            "offline",
            attempt_id=attempt_id,
            reconnect_attempts=previous.reconnect_attempts,
            last_connected_at=previous.last_connected_at,
        )
        return True

    def snapshot(self) -> RealtimeConnectionStatus:
        return self._status

    def dispose(self) -> None:
        """Stop observation, invalidate views and leave a final offline status."""
        if self._disposed:
            return
        self._disposed = True
        for waiter in tuple(self._readiness_waiters):
            if not waiter.done():
                waiter.set_result(None)
        if self._status.state != "offline":
            previous = self._status
            self._change(
                "offline",
                attempt_id=previous.attempt_id,
                reconnect_attempts=previous.reconnect_attempts,
                last_connected_at=previous.last_connected_at,
            )


@dataclass(frozen=True, kw_only=True)
class RealtimeConnectionQuality:
    state: Literal["unknown", "healthy", "degraded", "offline"]
    attempt_id: int
    latency_milliseconds: float | None
    last_frame_age_seconds: float | None
    heartbeat_pending_seconds: float | None


class RealtimeConnectionQualityTracker:
    """Observe frames and correlated heartbeat responses on one execution context.

    This observer sends no heartbeats. Call snapshot periodically to evaluate
    silence; it emits transitions without needing its own timers or tasks.
    """

    def __init__(
        self,
        *,
        on_change: Callable[[RealtimeConnectionQuality], None],
        degraded_latency_milliseconds: float = 1000,
        recovery_latency_milliseconds: float = 500,
        silence_seconds: float = 30,
        heartbeat_timeout_seconds: float = 10,
    ):
        if not callable(on_change):
            raise ValueError("on_change must be callable")
        for name, value, low, high in (
            ("degraded_latency_milliseconds", degraded_latency_milliseconds, 1, 60000),
            ("recovery_latency_milliseconds", recovery_latency_milliseconds, 1, 60000),
            ("silence_seconds", silence_seconds, 0.1, 86400),
            ("heartbeat_timeout_seconds", heartbeat_timeout_seconds, 0.1, 86400),
        ):
            if (
                isinstance(value, bool)
                or not isinstance(value, (int, float))
                or not math.isfinite(value)
                or not low <= value <= high
            ):
                raise ValueError(f"{name} must be finite and between {low} and {high}")
        if recovery_latency_milliseconds > degraded_latency_milliseconds:
            raise ValueError("Recovery latency must not exceed degraded latency")
        self._on_change = on_change
        self._degraded_latency = degraded_latency_milliseconds
        self._recovery_latency = recovery_latency_milliseconds
        self._silence, self._heartbeat_timeout = silence_seconds, heartbeat_timeout_seconds
        self._attempt_id = 0
        self._online = False
        self._disposed = False
        self._last_frame: float | None = None
        self._latency: float | None = None
        self._latency_degraded = False
        self._probe_id = 0
        self._pending: tuple[int, float, float] | None = None
        self._state: Literal["unknown", "healthy", "degraded", "offline"] = "offline"

    @staticmethod
    def _clock() -> float:
        return time.monotonic()

    def start(self, attempt_id: int) -> bool:
        """Call after authenticated transport readiness with its increasing attempt ID."""
        if type(attempt_id) is not int or attempt_id < 1:
            raise ValueError("attempt_id must be a positive integer")
        if self._disposed:
            raise ValueError("Connection quality tracker is disposed")
        if attempt_id <= self._attempt_id:
            return False
        self._attempt_id = attempt_id
        self._online = True
        self._last_frame = self._clock()
        self._latency = None
        self._latency_degraded = False
        self._pending = None
        previous = self._state
        quality = self.snapshot()
        if quality.state == previous:
            self._notify(quality)
        return True

    def _current(self, attempt_id: int) -> bool:
        if type(attempt_id) is not int or attempt_id < 1:
            raise ValueError("attempt_id must be a positive integer")
        return not self._disposed and self._online and attempt_id == self._attempt_id

    def frame_received(self, attempt_id: int) -> bool:
        if not self._current(attempt_id):
            return False
        self._last_frame = self._clock()
        self.snapshot()
        return True

    def heartbeat_sent(self, attempt_id: int) -> int | None:
        """Record an actual send; return a local correlation token for its response.

        A newer send replaces the outstanding probe; old responses are ignored.
        """
        if not self._current(attempt_id):
            return None
        self._probe_id += 1
        now = self._clock()
        oldest = self._pending[2] if self._pending is not None else now
        self._pending = self._probe_id, now, oldest
        self.snapshot()
        return self._probe_id

    def heartbeat_received(self, attempt_id: int, probe_id: int) -> bool:
        if type(probe_id) is not int or probe_id < 1:
            raise ValueError("probe_id must be a positive integer")
        if not self._current(attempt_id) or self._pending is None or self._pending[0] != probe_id:
            return False
        now = self._clock()
        self._latency = max(0.0, now - self._pending[1]) * 1000
        self._pending = None
        self._last_frame = now
        if self._latency >= self._degraded_latency:
            self._latency_degraded = True
        elif self._latency <= self._recovery_latency:
            self._latency_degraded = False
        previous = self._state
        quality = self.snapshot()
        if quality.state == previous:
            self._notify(quality)
        return True

    def snapshot(self) -> RealtimeConnectionQuality:
        now = self._clock()
        age = max(0.0, now - self._last_frame) if self._online and self._last_frame is not None else None
        pending_age = max(0.0, now - self._pending[2]) if self._online and self._pending is not None else None
        state = (
            "offline"
            if not self._online
            else "degraded"
            if self._latency_degraded
            or age is not None
            and age >= self._silence
            or pending_age is not None
            and pending_age >= self._heartbeat_timeout
            else "unknown"
            if self._latency is None
            else "healthy"
        )
        quality = RealtimeConnectionQuality(
            state=state,
            attempt_id=self._attempt_id,
            latency_milliseconds=self._latency,
            last_frame_age_seconds=age,
            heartbeat_pending_seconds=pending_age,
        )
        if state != self._state:
            self._state = state
            self._notify(quality)
        return quality

    def _notify(self, quality: RealtimeConnectionQuality) -> None:
        RealtimeConnectionStatusTracker._callback(self._on_change, quality)

    def disconnected(self, attempt_id: int) -> bool:
        if not self._current(attempt_id):
            return False
        self._online = False
        self._pending = None
        self.snapshot()
        return True

    def dispose(self) -> None:
        if self._disposed:
            return
        self._disposed = True
        self._online = False
        self._pending = None
        self.snapshot()
