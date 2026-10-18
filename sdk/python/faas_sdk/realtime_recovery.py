"""Ordered reconnect/replay orchestration over an application-supplied transport."""

from __future__ import annotations

import asyncio
import math
import random
import time
from collections.abc import AsyncIterator, Awaitable, Callable
from dataclasses import dataclass, field
from typing import Any, Protocol

from .realtime_backpressure import RealtimeProcessingStatus
from .realtime_catch_up import RealtimeCatchUpTracker, _sequence
from .realtime_connection_status import RealtimeConnectionStatusTracker
from .realtime_drain import RealtimeDrainResult, _RecoveryDrained
from .realtime_filters import _filter_fingerprint
from .realtime_subscriptions import RealtimeSubscriptionTracker, _identity
from .realtime_timeline import RealtimeIncidentTimeline


class _RecoveryPaused(Exception):
    """Internal cooperative stop at a completed processing boundary."""


class RealtimeRecoveryRetry(Exception):
    """Adapter explicitly classifies a transport failure as safe to reconnect."""


class RealtimeRecoveryError(Exception):
    """Recovery stopped; reason excludes adapter and application exception text."""

    def __init__(self, reason: str):
        super().__init__(f"Realtime recovery stopped: {reason}")
        self.reason = reason


@dataclass(frozen=True, kw_only=True)
class RealtimeRecoveryEvent:
    sequence: int
    data: Any = field(repr=False)
    checkpoint: bool = False


@dataclass(frozen=True, kw_only=True)
class RealtimeRecoveryTarget:
    oldest_sequence: int
    latest_sequence: int
    resume_sequence: int | None = None
    resync_required: bool = False
    filter_fingerprint: str | None = None


class RealtimeRecoverySession(Protocol):
    """One stream, one authenticated endpoint/principal, bounded live buffering.

    subscribe starts buffering live events BEFORE capturing authoritative history
    bounds. replay yields ordered retained events in (after, through], and only
    finishes after confirming that entire range (including holes) was scanned.
    events drains buffered/live events in order; overflow raises RecoveryRetry.
    resume_sequence optionally supplies an authoritative persisted checkpoint;
    resync_required forces snapshot recovery even when local progress is newer.
    Neither substitutes for the authoritative replay high-water target.
    """

    async def subscribe(self, *, channel: str | None, inbox: bool, after: int) -> RealtimeRecoveryTarget: ...

    def replay(self, *, after: int, through: int) -> AsyncIterator[RealtimeRecoveryEvent]: ...

    def events(self) -> AsyncIterator[RealtimeRecoveryEvent]: ...

    async def aclose(self) -> None: ...


class RealtimeRecoveryCoordinator:
    """Own one run at a time; cancel run to stop and close its session.

    Cursor advances only after successful application processing. Persist it in
    the processing transaction when restart durability is needed. No exactly-once
    side-effect guarantee is implied. Public trackers are observation-only here.
    """

    def __init__(
        self,
        *,
        connect: Callable[[], Awaitable[RealtimeRecoverySession]],
        process: Callable[[RealtimeRecoveryEvent], Awaitable[None]],
        load_snapshot: Callable[[RealtimeRecoveryTarget], Awaitable[int]] | None = None,
        channel: str | None = None,
        inbox: bool = False,
        processed_sequence: int = 0,
        cursor_filter_fingerprint: str | None = None,
        timeline: RealtimeIncidentTimeline | None = None,
        on_ready: Callable[[int], None] | None = None,
        initial_delay_seconds: float = 0.5,
        max_delay_seconds: float = 30,
        max_retries: int = 20,
        operation_timeout_seconds: float = 30,
    ):
        _identity(channel, inbox)
        _sequence(processed_sequence)
        _filter_fingerprint(cursor_filter_fingerprint)
        self._cursor_filter_fingerprint = cursor_filter_fingerprint
        for callback in (connect, process):
            if not callable(callback):
                raise ValueError("Recovery callbacks must be callable")
        if any(value is not None and not callable(value) for value in (load_snapshot, on_ready)):
            raise ValueError("Optional recovery callbacks must be callable")
        for value in (initial_delay_seconds, max_delay_seconds, operation_timeout_seconds):
            if type(value) not in (int, float) or not math.isfinite(value) or not 0.1 <= value <= 86400:
                raise ValueError("Recovery durations must be between 0.1 and 86400")
        if initial_delay_seconds > max_delay_seconds or type(max_retries) is not int or not 0 <= max_retries <= 1000:
            raise ValueError("Invalid recovery retry bounds")
        if timeline is not None and not isinstance(timeline, RealtimeIncidentTimeline):
            raise ValueError("Invalid incident timeline")
        self._connect, self._process, self._load = connect, process, load_snapshot
        self._channel, self._inbox, self._cursor = channel, inbox, processed_sequence
        self._on_ready = on_ready
        self._initial, self._maximum, self._retries = initial_delay_seconds, max_delay_seconds, max_retries
        self._timeout = operation_timeout_seconds
        self._running = False
        self._run_task: asyncio.Task | None = None
        self._pause_requested = False
        self._processing = False
        self._processing_since: float | None = None
        self._last_handler_duration: float | None = None
        self._paused = False
        self._session: RealtimeRecoverySession | None = None
        self._drain_requested = False
        self._drain_completed = False
        self._run_error: str | None = None
        self._run_cancelled = False
        self._cleanup_failed = False
        self._drain_task: asyncio.Task[RealtimeDrainResult] | None = None
        self.connection = RealtimeConnectionStatusTracker(
            on_change=timeline.record_connection if timeline else lambda _: None
        )
        self.subscriptions = RealtimeSubscriptionTracker(
            on_change=timeline.record_subscriptions if timeline else lambda _: None
        )
        self.catch_up = RealtimeCatchUpTracker(
            channel=channel, inbox=inbox, on_change=timeline.record_catch_up if timeline else lambda _: None
        )

    @property
    def processed_sequence(self) -> int:
        return self._cursor

    @property
    def cursor_filter_fingerprint(self) -> str | None:
        return self._cursor_filter_fingerprint

    def processing_snapshot(self) -> RealtimeProcessingStatus:
        """Handler timing for WebSocket or SSE; excludes queue wait and ACKs."""
        age = max(0, time.monotonic() - self._processing_since) if self._processing_since is not None else None
        return RealtimeProcessingStatus(
            processing=self._processing,
            active_handler_age_seconds=age,
            last_handler_duration_seconds=self._last_handler_duration,
        )

    @property
    def paused(self) -> bool:
        """True only after a requested pause has finished session cleanup."""
        return self._paused

    async def pause(self) -> int:
        """Finish the current processor, close the session and return its cursor.

        Call from outside the processor/run task. A hung processor must be stopped
        explicitly by cancelling run; pause never cancels application processing.
        Resume by calling run again. This also works with SSE adapters.
        """
        if self._drain_requested:
            raise RealtimeRecoveryError("drain_in_progress")
        task = self._run_task
        if task is asyncio.current_task():
            raise ValueError("Pause must be called outside the recovery task")
        self._pause_requested = True
        if task is None or task.done():
            self._paused = True
            return self._cursor
        if not self._processing:
            task.cancel()
        # Caller cancellation leaves the requested pause and cleanup running.
        await asyncio.shield(task)
        return self._cursor

    async def drain(self, *, deadline_seconds: float = 30) -> RealtimeDrainResult:
        """Drain native WebSocket delivery, then close; call outside run/process.

        The deadline bounds the run, including processing/ACKs and cleanup. On
        expiry, owned cleanup is joined using its existing timeout. Caller cancellation leaves
        the drain running. Cooperative handler cancellation is required.
        """
        if (
            type(deadline_seconds) not in (int, float)
            or not math.isfinite(deadline_seconds)
            or not 0.1 <= deadline_seconds <= 86400
        ):
            raise ValueError("Invalid drain deadline")
        if asyncio.current_task() is self._run_task:
            raise ValueError("Drain must be called outside the recovery task")
        if self._drain_task is not None:
            return await asyncio.shield(self._drain_task)
        session, task = self._session, self._run_task
        if self._pause_requested or session is None or task is None or task.done():
            raise RealtimeRecoveryError("drain_not_ready")
        begin = getattr(session, "begin_drain", None)
        progress = getattr(session, "drain_progress", None)
        if not callable(begin) or not callable(progress):
            raise RealtimeRecoveryError("drain_unsupported")
        begin()
        self._drain_requested = True

        async def finish() -> RealtimeDrainResult:
            _, pending = await asyncio.wait({task}, timeout=deadline_seconds)
            expired = bool(pending)
            if expired:
                task.cancel()
            reason = None
            try:
                await asyncio.shield(task)
            except asyncio.CancelledError:
                reason = "drain_deadline" if expired else "drain_cancelled"
            except RealtimeRecoveryError as exc:
                reason = exc.reason
            except Exception:
                reason = "drain_failed"
            if reason is None:
                reason = self._run_error or (
                    "drain_cancelled"
                    if self._run_cancelled
                    else "drain_cleanup_failed"
                    if self._cleanup_failed
                    else None
                )
            if expired:
                reason = "drain_deadline"
            unfinished, sent, confirmed = progress(self._cursor)
            durable = getattr(session, "_durable_subscription", None) is not None
            complete = (
                self._drain_completed
                and not expired
                and reason is None
                and unfinished == 0
                and sent
                and (not durable or confirmed is not None and confirmed >= self._cursor)
            )
            return RealtimeDrainResult(
                complete=complete,
                processed_sequence=self._cursor,
                confirmed_sequence=confirmed,
                final_ack_sent=sent,
                deadline_expired=expired,
                unfinished_event_count=unfinished,
                error_reason=reason if reason is not None else None if complete else "drain_incomplete",
            )

        self._drain_task = asyncio.create_task(finish())
        return await asyncio.shield(self._drain_task)

    def _pause_boundary(self) -> None:
        if self._pause_requested:
            raise _RecoveryPaused()

    @staticmethod
    async def _ordered(events: AsyncIterator[RealtimeRecoveryEvent]) -> AsyncIterator[RealtimeRecoveryEvent]:
        previous = -1
        async for event in events:
            if not isinstance(event, RealtimeRecoveryEvent):
                raise RealtimeRecoveryError("invalid_event")
            _sequence(event.sequence)
            if event.sequence < previous:
                raise RealtimeRecoveryError("unordered_events")
            previous = event.sequence
            yield event

    async def _apply(self, event: RealtimeRecoveryEvent, generation: int, *, through: int | None = None) -> None:
        self._pause_boundary()
        if not isinstance(event, RealtimeRecoveryEvent):
            raise RealtimeRecoveryError("invalid_event")
        _sequence(event.sequence)
        if through is not None and event.sequence > through:
            raise RealtimeRecoveryError("invalid_replay_range")
        if event.sequence <= self._cursor:
            return
        if type(event.checkpoint) is not bool:
            raise RealtimeRecoveryError("invalid_checkpoint")
        if not event.checkpoint:
            self._processing = True
            self._processing_since = time.monotonic()
            try:
                await self._process(event)
            except Exception:
                raise RealtimeRecoveryError("processing_failed") from None
            finally:
                self._last_handler_duration = max(0, time.monotonic() - self._processing_since)
                self._processing_since = None
                self._processing = False
        self._cursor = event.sequence
        self.catch_up.processed(generation, self._cursor)
        self._pause_boundary()

    async def _recover(self, session: RealtimeRecoverySession, attempt: int) -> None:
        subscription = self.subscriptions.begin(
            attempt, channels=() if self._inbox else (self._channel,), include_inbox=self._inbox
        )
        generation = self.catch_up.begin(attempt, processed_sequence=self._cursor)
        async with asyncio.timeout(self._timeout):
            target = await session.subscribe(channel=self._channel, inbox=self._inbox, after=self._cursor)
        if not isinstance(target, RealtimeRecoveryTarget):
            raise RealtimeRecoveryError("invalid_target")
        _filter_fingerprint(target.filter_fingerprint)
        if self._cursor_filter_fingerprint != target.filter_fingerprint:
            if self._cursor_filter_fingerprint is not None or self._cursor > 0:
                raise RealtimeRecoveryError("cursor_filter_mismatch")
            self._cursor_filter_fingerprint = target.filter_fingerprint
        _sequence(target.oldest_sequence)
        _sequence(target.latest_sequence)
        if not 1 <= target.oldest_sequence <= target.latest_sequence + 1 or target.latest_sequence < self._cursor:
            raise RealtimeRecoveryError("invalid_target")
        if type(target.resync_required) is not bool:
            raise RealtimeRecoveryError("invalid_target")
        if target.resume_sequence is not None:
            _sequence(target.resume_sequence)
            if target.resume_sequence > target.latest_sequence:
                raise RealtimeRecoveryError("invalid_target")
            self._cursor = max(self._cursor, target.resume_sequence)
            generation = self.catch_up.begin(attempt, processed_sequence=self._cursor)
        self.subscriptions.subscribed(subscription, channel=self._channel, inbox=self._inbox)
        if target.resync_required or self._cursor < target.oldest_sequence - 1:
            self.catch_up.require_resync(
                generation, oldest_sequence=target.oldest_sequence, latest_sequence=target.latest_sequence
            )
            if self._load is None:
                raise RealtimeRecoveryError("snapshot_required")
            try:
                async with asyncio.timeout(self._timeout):
                    position = await self._load(target)
                _sequence(position)
                if not target.oldest_sequence - 1 <= position <= target.latest_sequence:
                    raise ValueError("Invalid snapshot position")
            except Exception:
                raise RealtimeRecoveryError("snapshot_failed") from None
            self._cursor = position
            generation = self.catch_up.begin(attempt, processed_sequence=self._cursor)
        self.catch_up.set_target(generation, target.latest_sequence)
        async with asyncio.timeout(self._timeout):
            async for event in self._ordered(session.replay(after=self._cursor, through=target.latest_sequence)):
                await self._apply(event, generation, through=target.latest_sequence)
        # Successful full-range scan establishes holes as processed positions.
        self._cursor = target.latest_sequence
        self.catch_up.processed(generation, self._cursor)
        if self._on_ready is not None:
            RealtimeConnectionStatusTracker._callback(self._on_ready, self._cursor)
        async for event in self._ordered(session.events()):
            await self._apply(event, generation)
        raise RealtimeRecoveryRetry()

    async def _close(self, session: RealtimeRecoverySession) -> None:
        async def close() -> None:
            try:
                async with asyncio.timeout(self._timeout):
                    await session.aclose()
            except Exception:
                self._cleanup_failed = True

        task = asyncio.create_task(close())
        cancelled = False
        try:
            while not task.done():
                try:
                    await asyncio.shield(task)
                except asyncio.CancelledError:
                    cancelled = True
                    continue
        finally:
            # Retrieve completion without exposing adapter error text.
            if not task.cancelled():
                task.exception()
        if cancelled:
            raise asyncio.CancelledError

    async def run(self) -> None:
        """Run until cancellation/fatal failure; retry budget is total per run."""
        if self._running:
            raise ValueError("Recovery coordinator already running")
        if self._drain_task is not None and not self._drain_task.done():
            raise ValueError("Drain cleanup is still running")
        self._running = True
        self._run_task = asyncio.current_task()
        self._drain_requested = False
        self._drain_completed = False
        self._run_error = None
        self._run_cancelled = False
        self._cleanup_failed = False
        self._drain_task = None
        self._pause_requested = self._paused = False
        failures = 0
        try:
            while True:
                attempt = self.connection.begin_connect()
                session = None
                retry = False
                try:
                    async with asyncio.timeout(self._timeout):
                        session = await self._connect()
                    self._session = session
                    self.connection.connected(attempt)
                    await self._recover(session, attempt)
                except _RecoveryPaused:
                    return
                except _RecoveryDrained:
                    self._drain_completed = True
                    return
                except RealtimeRecoveryRetry:
                    if self._drain_requested:
                        raise RealtimeRecoveryError("drain_transport_lost") from None
                    retry = True
                except RealtimeRecoveryError:
                    raise
                except asyncio.CancelledError:
                    raise
                except Exception:
                    raise RealtimeRecoveryError("adapter_failed") from None
                finally:
                    self.connection.disconnected(attempt)
                    if session is not None:
                        self._cleanup_failed = False
                        await self._close(session)
                    self._session = None
                if retry:
                    if failures >= self._retries:
                        raise RealtimeRecoveryError("retry_exhausted") from None
                    delay = min(self._maximum, self._initial * 2 ** min(failures, 32))
                    failures += 1
                    await asyncio.sleep(random.uniform(0, delay))
        except RealtimeRecoveryError as exc:
            self._run_error = exc.reason
            raise
        except asyncio.CancelledError:
            self._run_cancelled = True
            if not self._pause_requested:
                raise
        finally:
            self._paused = self._pause_requested
            self._run_task = None
            self._session = None
            self._running = False
