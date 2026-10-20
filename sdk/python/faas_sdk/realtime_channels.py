"""Independent channel recovery over one authenticated managed v2 socket."""

from __future__ import annotations

import asyncio
import json
import math
from collections.abc import Awaitable, Callable
from typing import Literal

from .realtime_backpressure import RealtimeBackpressureMonitor
from .realtime_catch_up import _sequence
from .realtime_connection_status import RealtimeConnectionStatusTracker
from .realtime_diagnostics import export_realtime_diagnostics
from .realtime_drain import RealtimeDrainResult
from .realtime_filters import RealtimeEventFilter, _filter_fingerprint
from .realtime_health import summarize_realtime_health
from .realtime_recovery import (
    RealtimeRecoveryCoordinator,
    RealtimeRecoveryError,
    RealtimeRecoveryEvent,
    RealtimeRecoveryRetry,
    RealtimeRecoveryTarget,
)
from .realtime_subscriptions import _identity
from .realtime_timeline import RealtimeIncidentTimeline
from .realtime_websocket import RealtimeWebSocketClient, RealtimeWebSocketSession, _durable_subscription
from .realtime_websocket_presence import RealtimeWebSocketPresence
from .realtime_websocket_reads import RealtimeWebSocketReadProgress

_MAX_CHANNELS = 8  # Existing managed v2 per-connection channel limit.


async def _join(task: asyncio.Task) -> None:
    """Join owned cleanup despite repeated caller cancellation, then propagate it."""
    cancelled = False
    while not task.done():
        try:
            await asyncio.shield(task)
        except asyncio.CancelledError:
            cancelled = True
    if not task.cancelled():
        task.result()
    if cancelled:
        raise asyncio.CancelledError


class _ChannelSession(RealtimeWebSocketSession):
    def __init__(self, pool: _SocketPool, channel: str):
        super().__init__(pool.socket, consumer=None, buffer_size=pool.transport._buffer)
        self._pool = pool
        self._channel = channel
        # Shared reader routes directly to _frame; never start a second recv loop.
        self._reader = pool.reader
        self._removed = asyncio.Event()
        self._closing: asyncio.Task[None] | None = None
        self._on_failure: Callable[[], None] | None = None

    async def _send(self, frame: dict) -> None:
        if frame.get("type") == "subscribe" and self._pool.transport._direct_messages is not None:
            frame = {**frame, "direct_messages": True}
        await super()._send(frame)

    async def aclose(self) -> None:
        if self._closing is None:
            self.backpressure._invalidate(self)
            if self._presence is not None:
                self._presence._invalidate(self)
            if self._read_progress is not None:
                self._read_progress._invalidate(self)
            self._closed = True
            self._changed.set()
            self._closing = asyncio.create_task(self._pool.remove(self))
        await _join(self._closing)
        if self._read_progress is not None:
            await self._read_progress._join_refresh()

    def fail(self, error: Exception) -> None:
        self.backpressure._invalidate(self)
        if self._presence is not None:
            self._presence._invalidate(self)
        if self._read_progress is not None:
            self._read_progress._invalidate(self)
        self._failure = error
        self._changed.set()
        if self._on_failure is not None:
            self._on_failure()


class _SocketPool:
    def __init__(self, transport: RealtimeWebSocketClient, timeout: float):
        self.transport, self.timeout = transport, timeout
        self.socket = None
        self.reader: asyncio.Task[None] | None = None
        self.sessions: dict[str, _ChannelSession] = {}
        self.lock = asyncio.Lock()
        self.closed = False
        self.failed = False

    def _fail(self, error: Exception) -> None:
        self.failed = True
        if self.transport._direct_messages is not None:
            self.transport._direct_messages._invalidate(self.socket)
        for session in tuple(self.sessions.values()):
            session.fail(error)
            session._removed.set()

    async def _drop(self) -> None:
        socket, reader = self.socket, self.reader
        self._fail(RealtimeRecoveryRetry())
        self.socket = self.reader = None
        self.sessions.clear()

        async def finish() -> None:
            if self.transport._direct_messages is not None and socket is not None:
                await self.transport._direct_messages._join(socket)
            if reader is not None:
                reader.cancel()
                await asyncio.gather(reader, return_exceptions=True)
            if socket is not None:
                try:
                    await socket.close()
                except Exception:
                    pass

        await _join(asyncio.create_task(finish()))

    async def acquire(self, channel: str) -> _ChannelSession:
        async with self.lock:
            if self.closed:
                raise RealtimeRecoveryError("client_closed")
            if self.failed or self.reader is not None and self.reader.done():
                await self._drop()
            if self.socket is None:
                self.socket = await self.transport._open_socket()
                self.failed = False
                if self.transport._direct_messages is not None:
                    try:
                        self.transport._direct_messages._bind(self.socket, self._send_direct, self._fail)
                    except Exception:
                        await self._drop()
                        raise
                self.reader = asyncio.create_task(self._read())
            if channel in self.sessions:
                raise RealtimeRecoveryError("channel_already_subscribed")
            session = _ChannelSession(self, channel)
            self.sessions[channel] = session
            return session

    async def remove(self, session: _ChannelSession) -> None:
        async with self.lock:
            channel = session._channel
            if self.sessions.get(channel) is not session:
                return
            if not self.failed:
                try:
                    # Keep the old identity until a wire ACK prevents old frames
                    # from being delivered to a replacement subscription.
                    async with asyncio.timeout(self.timeout):
                        await session._send({"type": "unsubscribe", "channel": channel})
                        await session._removed.wait()
                except Exception:
                    await self._drop()
                    return
            self.sessions.pop(channel, None)
            if not self.sessions:
                await self._drop()

    async def _send_direct(self, frame: dict) -> None:
        try:
            await self.socket.send(json.dumps(frame, separators=(",", ":")))
        except Exception:
            raise RealtimeRecoveryRetry() from None

    async def _read(self) -> None:
        try:
            async for raw in self.socket:
                if not isinstance(raw, str):
                    raise RealtimeRecoveryError("invalid_frame")
                frame = json.loads(raw, parse_constant=lambda _: (_ for _ in ()).throw(ValueError()))
                if isinstance(frame, dict) and frame.get("type") in (
                    "direct_message",
                    "direct_message_acknowledged",
                    "direct_message_ack_failed",
                ):
                    if self.transport._direct_messages is None:
                        raise RealtimeRecoveryError("unexpected_direct_message")
                    self.transport._direct_messages._receive(self.socket, frame)
                    continue
                if not isinstance(frame, dict) or not isinstance(frame.get("channel"), str):
                    raise RealtimeRecoveryError("unexpected_stream")
                session = self.sessions.get(frame["channel"])
                if session is None:
                    if frame.get("type") in ("presence", "signal", "read_receipt", "acknowledged"):
                        continue
                    raise RealtimeRecoveryError("unexpected_stream")
                if session._closed:
                    if frame.get("type") == "unsubscribed" or (
                        frame.get("type") == "error" and frame.get("code") == "not_subscribed"
                    ):
                        session._removed.set()
                    continue
                if session._failure is not None:
                    continue
                try:
                    await session._frame(frame)
                except (RealtimeRecoveryError, RealtimeRecoveryRetry) as exc:
                    session.fail(exc)
                except Exception:
                    session.fail(RealtimeRecoveryError("invalid_frame"))
                session._changed.set()
            self._fail(RealtimeRecoveryRetry())
        except asyncio.CancelledError:
            raise
        except (RealtimeRecoveryError, RealtimeRecoveryRetry) as exc:
            self._fail(exc)
        except Exception as exc:
            from websockets.exceptions import ConnectionClosed

            self._fail(
                RealtimeRecoveryRetry()
                if isinstance(exc, ConnectionClosed) and exc.code not in (1002, 1003, 1007, 1008, 1009)
                else RealtimeRecoveryError("invalid_frame")
            )

    async def aclose(self) -> None:
        async with self.lock:
            self.closed = True
            await self._drop()


class RealtimeChannelSubscription:
    """One channel's completed cursor, readiness, error and redacted diagnostics."""

    def __init__(self, channel: str, recovery: RealtimeRecoveryCoordinator, timeline: RealtimeIncidentTimeline):
        self.channel, self.recovery, self.timeline = channel, recovery, timeline
        self._changed = asyncio.Event()
        self._task: asyncio.Task[None] | None = None
        self._error: RealtimeRecoveryError | None = None
        self._closed = False
        self._ready_generation: tuple[int, int] | None = None
        self._stopping: asyncio.Task[None] | None = None
        self._pausing: asyncio.Task[int] | None = None
        self._draining: asyncio.Task[RealtimeDrainResult] | None = None
        self._on_drained: Callable[[], Awaitable[int]] | None = None
        self.presence: RealtimeWebSocketPresence | None = None
        self.read_progress: RealtimeWebSocketReadProgress | None = None
        self.durable_subscription: str | None = None
        self.confirmed_sequence: int | None = None
        self.event_filter: RealtimeEventFilter | None = None
        self.backpressure = RealtimeBackpressureMonitor()

    @property
    def processed_sequence(self) -> int:
        return self.recovery.processed_sequence

    @property
    def cursor_filter_fingerprint(self) -> str | None:
        return self.recovery.cursor_filter_fingerprint

    @property
    def ready(self) -> bool:
        return (
            not self._closed
            and self._pausing is None
            and self._draining is None
            and not self.recovery.paused
            and self._error is None
            and self.recovery.connection.snapshot().state == "connected"
            and self.recovery.catch_up.snapshot().state == "caught_up"
            and self._ready_generation
            == (
                self.recovery.catch_up.snapshot().attempt_id,
                self.recovery.catch_up.snapshot().generation,
            )
        )

    @property
    def state(self) -> Literal["recovering", "live", "pausing", "paused", "draining", "failed", "closed"]:
        if self._closed:
            return "closed"
        if self._draining is not None:
            return "draining"
        if self._error is not None:
            return "failed"
        if self._pausing is not None and not self._pausing.done():
            return "pausing"
        if self.recovery.paused and (self._task is None or self._task.done()):
            return "paused"
        return "live" if self.ready else "recovering"

    async def pause(self) -> int:
        """Finish current processing and unsubscribe, retaining this handle."""
        if self._closed:
            raise RealtimeRecoveryError("subscription_closed")
        if self._draining is not None:
            raise RealtimeRecoveryError("drain_in_progress")
        if self._error is not None:
            raise self._error
        if asyncio.current_task() is self._task:
            raise ValueError("Pause must be called outside the channel processor")
        if self._pausing is None:
            self._ready_generation = None
            self._changed.set()
            self._pausing = asyncio.create_task(self.recovery.pause())
            self._pausing.add_done_callback(lambda _: self._changed.set())
        cursor = await asyncio.shield(self._pausing)
        self._changed.set()
        if self._error is not None:
            raise self._error
        return cursor

    async def resume(self) -> None:
        """Restart replay; await wait_ready for live delivery to become ready."""
        if self._closed:
            raise RealtimeRecoveryError("subscription_closed")
        if self._draining is not None:
            raise RealtimeRecoveryError("drain_in_progress")
        if asyncio.current_task() is self._task:
            raise ValueError("Resume must be called outside the channel processor")
        if self._pausing is not None:
            await asyncio.shield(self._pausing)
        if self._closed:
            raise RealtimeRecoveryError("subscription_closed")
        if self._error is not None:
            raise self._error
        if self._task is not None and not self._task.done():
            return
        self._pausing = None
        self._ready_generation = None
        self._task = asyncio.create_task(self._run())
        self._changed.set()

    async def drain(self, *, deadline_seconds: float = 30) -> RealtimeDrainResult:
        """Drain this channel, flush its final ACK, then remove the handle."""
        if asyncio.current_task() is self._task:
            raise ValueError("Drain must be called outside the channel processor")
        if self._draining is not None:
            return await asyncio.shield(self._draining)
        if self._closed:
            raise RealtimeRecoveryError("subscription_closed")
        if self._pausing is not None or self.recovery.paused or self._error is not None:
            raise RealtimeRecoveryError("drain_not_ready")
        if (
            type(deadline_seconds) not in (int, float)
            or not math.isfinite(deadline_seconds)
            or not 0.1 <= deadline_seconds <= 86400
        ):
            raise ValueError("Invalid drain deadline")

        session = self.recovery._session
        if session is None or not session._active or session._closed or session._failure is not None:
            raise RealtimeRecoveryError("drain_not_ready")
        previous_generation = self._ready_generation

        async def finish() -> RealtimeDrainResult:
            try:
                result = await self.recovery.drain(deadline_seconds=deadline_seconds)
            except BaseException:
                if not self.recovery._drain_requested:
                    self._draining = None
                    self._ready_generation = previous_generation
                raise
            finally:
                if self.recovery._drain_requested and self._on_drained is not None:
                    await self._on_drained()
            return result

        def done(task: asyncio.Task) -> None:
            if not task.cancelled():
                task.exception()
            self._changed.set()

        self._ready_generation = None
        self._draining = asyncio.create_task(finish())
        self._draining.add_done_callback(done)
        self._changed.set()
        return await asyncio.shield(self._draining)

    @property
    def error(self) -> RealtimeRecoveryError | None:
        return self._error

    def diagnostics(self) -> str:
        presence = self.presence.snapshot() if self.presence is not None else None
        unread = self.read_progress.summary() if self.read_progress is not None else None
        return export_realtime_diagnostics(
            summarize_realtime_health(
                self.recovery.connection.snapshot(),
                subscriptions=self.recovery.subscriptions.snapshot(),
                catch_up=(self.recovery.catch_up.snapshot(),),
                presence=(presence,) if presence is not None else (),
                unread=unread,
            ),
            timeline=self.timeline,
            unread=unread,
            backpressure=self.backpressure.snapshot(),
            processing=self.recovery.processing_snapshot(),
        )

    async def wait_ready(self, *, timeout_seconds: float | None = None) -> int:
        """Wait for this channel only; cancellation leaves its recovery running."""
        if timeout_seconds is not None and (
            type(timeout_seconds) not in (int, float)
            or not math.isfinite(timeout_seconds)
            or not 0 <= timeout_seconds <= 86400
        ):
            raise ValueError("timeout_seconds must be between 0 and 86400, or None")
        async with asyncio.timeout(timeout_seconds):
            while True:
                self._changed.clear()
                if self._closed:
                    raise RealtimeRecoveryError("subscription_closed")
                if self._error is not None:
                    raise self._error
                if self.ready:
                    return self.processed_sequence
                await self._changed.wait()

    async def _run(self) -> None:
        try:
            await self.recovery.run()
        except asyncio.CancelledError:
            raise
        except RealtimeRecoveryError as exc:
            self._error = exc
        except Exception:
            self._error = RealtimeRecoveryError("recovery_failed")
        finally:
            self._changed.set()


class RealtimeMultiChannelClient:
    """Dynamically consume up to eight channels on one socket and one event loop.

    Per-channel processors run independently. Transport loss affects all streams;
    processing failures, retention gaps and queue overflow affect their channel.
    Unsubscribe/close joins cooperative processing and removes server subscriptions.
    """

    def __init__(
        self,
        *,
        transport: RealtimeWebSocketClient,
        operation_timeout_seconds: float = 30,
        max_retries: int = 20,
    ):
        if not isinstance(transport, RealtimeWebSocketClient):
            raise ValueError("A native WebSocket transport is required")
        if transport._backpressure is not None:
            raise ValueError("Configure backpressure on each channel subscription")
        if (
            type(operation_timeout_seconds) not in (int, float)
            or not math.isfinite(operation_timeout_seconds)
            or not 0.1 <= operation_timeout_seconds <= 86400
            or type(max_retries) is not int
            or not 0 <= max_retries <= 1000
        ):
            raise ValueError("Invalid recovery timeout or retry budget")
        self._pool = _SocketPool(transport, operation_timeout_seconds)
        self._timeout, self._retries = operation_timeout_seconds, max_retries
        self._channels: dict[str, RealtimeChannelSubscription] = {}
        self._closed = False
        self._closing: asyncio.Task[None] | None = None

    def subscribe(
        self,
        channel: str,
        *,
        process: Callable[[RealtimeRecoveryEvent], Awaitable[None]],
        processed_sequence: int = 0,
        load_snapshot: Callable[[RealtimeRecoveryTarget], Awaitable[int]] | None = None,
        on_ready: Callable[[int], None] | None = None,
        presence: RealtimeWebSocketPresence | None = None,
        read_progress: RealtimeWebSocketReadProgress | None = None,
        durable_subscription: str | None = None,
        event_filter: RealtimeEventFilter | None = None,
        cursor_filter_fingerprint: str | None = None,
        backpressure: RealtimeBackpressureMonitor | None = None,
    ) -> RealtimeChannelSubscription:
        """Start channel recovery; await the returned handle's wait_ready for replay."""
        _identity(channel, False)
        if backpressure is not None and (
            not isinstance(backpressure, RealtimeBackpressureMonitor)
            or backpressure._owner is not None
            or any(entry.backpressure is backpressure for entry in self._channels.values())
        ):
            raise ValueError("Invalid or shared backpressure monitor")
        _durable_subscription(durable_subscription)
        _sequence(processed_sequence)
        _filter_fingerprint(cursor_filter_fingerprint)
        if event_filter is not None and not isinstance(event_filter, RealtimeEventFilter):
            raise ValueError("Invalid event filter")
        expected = event_filter.fingerprint if event_filter is not None and event_filter.values else None
        if cursor_filter_fingerprint != expected and (processed_sequence > 0 or cursor_filter_fingerprint is not None):
            raise ValueError("Restored cursor does not match the selected event filter")
        effective_subscription = (
            event_filter.scoped_subscription(durable_subscription)
            if event_filter is not None and durable_subscription is not None
            else durable_subscription
        )
        if self._closed:
            raise RealtimeRecoveryError("client_closed")
        if channel in self._channels or len(self._channels) >= _MAX_CHANNELS:
            raise ValueError("Duplicate channel or eight-channel capacity reached")
        if presence is not None:
            if not isinstance(presence, RealtimeWebSocketPresence) or presence._disposed:
                raise ValueError("Invalid or disposed presence binding")
            if presence._session is not None or any(entry.presence is presence for entry in self._channels.values()):
                raise ValueError("Presence bindings cannot be shared between subscriptions")
            if presence._channel is not None and presence._channel != channel:
                raise ValueError("Presence binding belongs to another channel")
        if read_progress is not None:
            if not isinstance(read_progress, RealtimeWebSocketReadProgress) or read_progress._disposed:
                raise ValueError("Invalid or disposed read-progress binding")
            if read_progress._session is not None or any(
                e.read_progress is read_progress for e in self._channels.values()
            ):
                raise ValueError("Read-progress bindings cannot be shared between subscriptions")
            if read_progress._stream is not None and read_progress._stream != (False, channel):
                raise ValueError("Read-progress binding belongs to another stream")
        loop = asyncio.get_running_loop()
        timeline = RealtimeIncidentTimeline()

        async def connect() -> _ChannelSession:
            attempt = recovery.connection.snapshot().attempt_id
            session = await self._pool.acquire(channel)

            def failed() -> None:
                recovery.connection.disconnected(attempt)
                subscription._changed.set()

            session.backpressure = subscription.backpressure
            session._durable_subscription = effective_subscription
            session._event_filter = event_filter
            session._checkpoint_timeout = self._pool.transport._checkpoint_timeout

            def checkpoint(sequence: int) -> None:
                subscription.confirmed_sequence = sequence

            session._on_checkpoint = checkpoint
            session._presence = presence
            session._read_progress = read_progress
            session._on_failure = failed
            return session

        def ready(cursor: int) -> None:
            status = subscription.recovery.catch_up.snapshot()
            subscription._ready_generation = status.attempt_id, status.generation
            subscription._changed.set()
            if on_ready is not None:
                RealtimeConnectionStatusTracker._callback(on_ready, cursor)

        recovery = RealtimeRecoveryCoordinator(
            connect=connect,
            process=process,
            load_snapshot=load_snapshot,
            channel=channel,
            processed_sequence=processed_sequence,
            cursor_filter_fingerprint=cursor_filter_fingerprint,
            timeline=timeline,
            on_ready=ready,
            operation_timeout_seconds=self._timeout,
            max_retries=self._retries,
        )
        subscription = RealtimeChannelSubscription(channel, recovery, timeline)
        if backpressure is not None:
            subscription.backpressure = backpressure
        subscription.presence = presence
        subscription.read_progress = read_progress
        subscription.durable_subscription = effective_subscription
        subscription.event_filter = event_filter
        subscription._on_drained = lambda: self._stop(subscription)
        self._channels[channel] = subscription
        subscription._task = loop.create_task(subscription._run())
        return subscription

    async def unsubscribe(self, channel: str) -> int:
        """Stop this channel and return its last completed position."""
        _identity(channel, False)
        subscription = self._channels.get(channel)
        if subscription is None:
            raise ValueError("Unknown channel")

        return await self._stop(subscription)

    async def _stop(self, subscription: RealtimeChannelSubscription) -> int:
        channel = subscription.channel

        async def finish() -> None:
            if subscription._pausing is not None:
                await asyncio.gather(subscription._pausing, return_exceptions=True)
            if subscription._task is not None:
                await asyncio.gather(subscription._task, return_exceptions=True)
            if subscription.presence is not None:
                subscription.presence.dispose()
            if subscription.read_progress is not None:
                subscription.read_progress.dispose()
            if self._channels.get(channel) is subscription:
                self._channels.pop(channel)

        if subscription._stopping is None:
            subscription._closed = True
            subscription._changed.set()
            if subscription._task is not None:
                subscription._task.cancel()
            subscription._stopping = asyncio.create_task(finish())
        await _join(subscription._stopping)
        return subscription.processed_sequence

    async def aclose(self) -> None:
        """Reject admission, stop all channels and close the shared connection."""
        self._closed = True
        if self._closing is None:

            async def finish() -> None:
                try:
                    await asyncio.gather(*(self._stop(subscription) for subscription in tuple(self._channels.values())))
                finally:
                    await self._pool.aclose()

            self._closing = asyncio.create_task(finish())
        await _join(self._closing)
