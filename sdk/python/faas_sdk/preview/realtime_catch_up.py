"""Observe processed replay progress against an explicit authoritative target."""

from __future__ import annotations

import asyncio
import math
import time
from collections.abc import Callable
from dataclasses import dataclass, replace
from typing import Literal

from .realtime_connection_status import RealtimeConnectionStatusTracker


@dataclass(frozen=True, kw_only=True)
class RealtimeCatchUpStatus:
    channel: str | None
    inbox: bool
    attempt_id: int
    generation: int
    state: Literal["idle", "waiting_target", "catching_up", "caught_up", "resync_required", "stalled"]
    processed_sequence: int
    target_sequence: int | None
    oldest_sequence: int | None = None
    latest_sequence: int | None = None
    progress_age_seconds: float | None = None
    stall_seconds: float = 0.0

    @property
    def remaining_sequence_distance(self) -> int | None:
        if self.target_sequence is None:
            return None
        return max(0, self.target_sequence - self.processed_sequence)


class RealtimeCatchUpWaitError(Exception):
    """Terminal catch-up wait result without transport or payload details."""

    def __init__(
        self,
        reason: Literal["stalled", "resync_required", "obsolete", "disposed"],
        status: RealtimeCatchUpStatus,
    ):
        super().__init__(f"Catch-up completion wait failed: {reason}")
        self.reason = reason
        self.status = status


def _sequence(value: int) -> None:
    if type(value) is not int or not 0 <= value <= 2**63 - 1:
        raise ValueError("Sequence must be a nonnegative signed 64-bit integer")


class RealtimeCatchUpTracker:
    """One channel or inbox on one execution context; no replay, ACK or resync writes.

    processed asserts application work through the position finished successfully.
    The caller owns ordered processing, deduplication and authoritative targets.
    """

    def __init__(
        self,
        *,
        on_change: Callable[[RealtimeCatchUpStatus], None],
        channel: str | None = None,
        inbox: bool = False,
        stall_after_seconds: float | None = None,
        max_waiters: int = 128,
    ):
        if not callable(on_change) or type(inbox) is not bool:
            raise ValueError("A callback and strict inbox flag are required")
        if (
            inbox
            and channel is not None
            or not inbox
            and (not isinstance(channel, str) or not channel or len(channel.encode()) > 256)
        ):
            raise ValueError("Choose a channel or the principal inbox")
        if stall_after_seconds is not None and (
            isinstance(stall_after_seconds, bool)
            or not isinstance(stall_after_seconds, (int, float))
            or not math.isfinite(stall_after_seconds)
            or not 0.1 <= stall_after_seconds <= 86400
        ):
            raise ValueError("stall_after_seconds must be between 0.1 and 86400, or None")
        if type(max_waiters) is not int or not 1 <= max_waiters <= 8192:
            raise ValueError("max_waiters must be an integer between 1 and 8192")
        self._max_waiters = max_waiters
        self._waiting_count = 0
        self._wait_loop: asyncio.AbstractEventLoop | None = None
        self._waiters: set[asyncio.Future[None]] = set()
        self._stall_after = stall_after_seconds
        self._last_progress: float | None = None
        self._on_change = on_change
        self._disposed = False
        self._status = RealtimeCatchUpStatus(
            channel=channel,
            inbox=inbox,
            attempt_id=0,
            generation=0,
            state="idle",
            processed_sequence=0,
            target_sequence=None,
        )

    def _set(self, status: RealtimeCatchUpStatus) -> None:
        if status.state == "catching_up":
            status = replace(status, progress_age_seconds=0.0)
        self._status = status
        self._wake_waiters()
        RealtimeConnectionStatusTracker._callback(self._on_change, status)

    def begin(self, attempt_id: int, *, processed_sequence: int, target_sequence: int | None = None) -> int | None:
        """Begin/restart a subscription; return a local generation for all its callbacks."""
        if self._disposed:
            raise ValueError("Catch-up tracker is disposed")
        if type(attempt_id) is not int or attempt_id < 1:
            raise ValueError("attempt_id must be a positive integer")
        _sequence(processed_sequence)
        if target_sequence is not None:
            _sequence(target_sequence)
        if attempt_id < self._status.attempt_id:
            return None
        previous = self._status
        state = (
            "waiting_target"
            if target_sequence is None
            else "caught_up"
            if processed_sequence >= target_sequence
            else "catching_up"
        )
        status = RealtimeCatchUpStatus(
            channel=previous.channel,
            inbox=previous.inbox,
            attempt_id=attempt_id,
            generation=previous.generation + 1,
            state=state,
            processed_sequence=processed_sequence,
            target_sequence=target_sequence,
        )
        self._last_progress = time.monotonic()
        self._set(status)
        return status.generation

    def _current(self, generation: int) -> bool:
        if type(generation) is not int or generation < 1:
            raise ValueError("generation must be a positive integer")
        return not self._disposed and generation == self._status.generation

    def set_target(self, generation: int, sequence: int) -> bool:
        _sequence(sequence)
        if not self._current(generation) or self._status.state == "resync_required":
            return False
        previous = self._status
        if previous.target_sequence is not None:
            if previous.target_sequence != sequence:
                raise ValueError("Catch-up target is frozen; begin a new generation to replace it")
            return False
        self._last_progress = time.monotonic()
        self._set(
            RealtimeCatchUpStatus(
                channel=previous.channel,
                inbox=previous.inbox,
                attempt_id=previous.attempt_id,
                generation=generation,
                state="caught_up" if previous.processed_sequence >= sequence else "catching_up",
                processed_sequence=previous.processed_sequence,
                target_sequence=sequence,
            )
        )
        return True

    def processed(self, generation: int, sequence: int) -> bool:
        _sequence(sequence)
        if (
            not self._current(generation)
            or self._status.state == "resync_required"
            or sequence <= self._status.processed_sequence
        ):
            return False
        previous = self._status
        state = (
            "waiting_target"
            if previous.target_sequence is None
            else "caught_up"
            if sequence >= previous.target_sequence
            else "catching_up"
        )
        self._last_progress = time.monotonic()
        self._set(
            RealtimeCatchUpStatus(
                channel=previous.channel,
                inbox=previous.inbox,
                attempt_id=previous.attempt_id,
                generation=generation,
                state=state,
                processed_sequence=sequence,
                target_sequence=previous.target_sequence,
            )
        )
        return True

    def require_resync(self, generation: int, *, oldest_sequence: int, latest_sequence: int) -> bool:
        _sequence(oldest_sequence)
        _sequence(latest_sequence)
        if not 1 <= oldest_sequence <= latest_sequence + 1:
            raise ValueError("Invalid retained-history bounds")
        if not self._current(generation):
            return False
        previous = self._status
        status = RealtimeCatchUpStatus(
            channel=previous.channel,
            inbox=previous.inbox,
            attempt_id=previous.attempt_id,
            generation=generation,
            state="resync_required",
            processed_sequence=previous.processed_sequence,
            target_sequence=previous.target_sequence,
            oldest_sequence=oldest_sequence,
            latest_sequence=latest_sequence,
        )
        if status == previous:
            return False
        self._set(status)
        return True

    def snapshot(self) -> RealtimeCatchUpStatus:
        """Evaluate progress age on an existing UI/lifecycle tick; own no timer."""
        previous = self._status
        if self._disposed or previous.state not in ("catching_up", "stalled") or self._last_progress is None:
            return previous
        age = max(0.0, time.monotonic() - self._last_progress)
        stalled = self._stall_after is not None and age >= self._stall_after
        status = replace(
            previous,
            state="stalled" if stalled else "catching_up",
            progress_age_seconds=age,
            stall_seconds=max(0.0, age - self._stall_after) if stalled else 0.0,
        )
        self._status = status
        if status.state != previous.state:
            self._wake_waiters()
            RealtimeConnectionStatusTracker._callback(self._on_change, status)
        return status

    def _wake_waiters(self) -> None:
        for waiter in tuple(self._waiters):
            if not waiter.done():
                waiter.set_result(None)

    async def wait_caught_up(
        self,
        generation: int,
        *,
        timeout_seconds: float | None = None,
        fail_on_stall: bool = True,
    ) -> RealtimeCatchUpStatus:
        """Await successfully processed progress to the frozen target for one generation.

        A configured stall deadline wakes this wait without requiring an external
        polling tick. Timeout/cancellation never advance or reset progress.
        """
        if type(generation) is not int or generation < 1:
            raise ValueError("generation must be a positive integer")
        if type(fail_on_stall) is not bool:
            raise ValueError("fail_on_stall must be boolean")
        if timeout_seconds is not None and (
            isinstance(timeout_seconds, bool)
            or not isinstance(timeout_seconds, (int, float))
            or not math.isfinite(timeout_seconds)
            or not 0 <= timeout_seconds <= 86400
        ):
            raise ValueError("timeout_seconds must be between 0 and 86400, or None")
        loop = asyncio.get_running_loop()
        if self._wait_loop is not None and self._wait_loop is not loop:
            raise ValueError("Catch-up waits must use one asyncio loop")
        if self._waiting_count >= self._max_waiters:
            raise ValueError("Catch-up waiter capacity reached")
        self._wait_loop = loop
        self._waiting_count += 1
        try:
            async with asyncio.timeout(timeout_seconds):
                while True:
                    self.snapshot()
                    # Re-read after monitoring callbacks, which may begin a new generation.
                    status = self._status
                    if self._disposed:
                        raise RealtimeCatchUpWaitError("disposed", status)
                    if generation != status.generation:
                        raise RealtimeCatchUpWaitError("obsolete", status)
                    if status.state == "resync_required":
                        raise RealtimeCatchUpWaitError("resync_required", status)
                    if status.state == "stalled" and fail_on_stall:
                        raise RealtimeCatchUpWaitError("stalled", status)
                    if status.state == "caught_up":
                        return status
                    delay = None
                    if (
                        fail_on_stall
                        and self._stall_after is not None
                        and status.state == "catching_up"
                        and self._last_progress is not None
                    ):
                        delay = max(0.0, self._last_progress + self._stall_after - time.monotonic())
                    waiter = loop.create_future()
                    self._waiters.add(waiter)
                    try:
                        if delay is None:
                            await waiter
                        else:
                            try:
                                await asyncio.wait_for(waiter, timeout=delay)
                            except TimeoutError:
                                pass
                    finally:
                        self._waiters.discard(waiter)
        finally:
            self._waiting_count -= 1

    def dispose(self) -> None:
        self._disposed = True
        self._wake_waiters()
