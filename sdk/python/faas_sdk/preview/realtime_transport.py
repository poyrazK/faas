"""Opt-in receive-only WebSocket/SSE transport selection."""

from __future__ import annotations

import math
import time
from collections.abc import AsyncIterator, Callable
from dataclasses import dataclass
from typing import Literal

from .realtime_recovery import (
    RealtimeRecoveryError,
    RealtimeRecoveryEvent,
    RealtimeRecoveryRetry,
    RealtimeRecoveryTarget,
)
from .realtime_sse import RealtimeSSEClient
from .realtime_websocket import RealtimeWebSocketClient


@dataclass(frozen=True, kw_only=True)
class RealtimeTransportStatus:
    transport: Literal["websocket", "sse"]
    websocket_failures: int
    fallback: bool


class RealtimeFallbackClient:
    """Pass connect to recovery. SSE supports retained channels and local filters.

    WebSocket probes occur on reconnect after the cooldown, never interrupting a
    healthy SSE stream. Application cursors remain owned by the coordinator.
    One factory belongs to one coordinator; callbacks must be fast and synchronous.
    """

    def __init__(
        self,
        *,
        websocket: RealtimeWebSocketClient,
        fallback_after: int = 2,
        probe_interval_seconds: float = 60,
        on_change: Callable[[RealtimeTransportStatus], None] | None = None,
    ):
        if not isinstance(websocket, RealtimeWebSocketClient):
            raise ValueError("A native WebSocket client is required")
        if any(
            value is not None
            for value in (
                websocket._consumer,
                websocket._durable_subscription,
                websocket._presence,
                websocket._read_progress,
                websocket._direct_messages,
            )
        ):
            raise ValueError("SSE fallback requires a receive-only, non-durable channel configuration")
        if type(fallback_after) is not int or not 1 <= fallback_after <= 100:
            raise ValueError("fallback_after must be between 1 and 100")
        if (
            type(probe_interval_seconds) not in (int, float)
            or not math.isfinite(probe_interval_seconds)
            or not 1 <= probe_interval_seconds <= 86400
        ):
            raise ValueError("Invalid WebSocket probe interval")
        if on_change is not None and not callable(on_change):
            raise ValueError("Invalid transport callback")
        self._ws = websocket
        self._sse = RealtimeSSEClient(
            url="https" + websocket._url[3:], token=websocket._token, event_filter=websocket._event_filter
        )
        self._threshold, self._interval, self._callback = fallback_after, probe_interval_seconds, on_change
        self._failures, self._probe_at = 0, 0.0
        self._transport: Literal["websocket", "sse"] = "websocket"

    @property
    def snapshot(self) -> RealtimeTransportStatus:
        return RealtimeTransportStatus(
            transport=self._transport, websocket_failures=self._failures, fallback=self._transport == "sse"
        )

    def _notify(self) -> None:
        if self._callback is not None:
            try:
                self._callback(self.snapshot)
            except Exception:
                pass

    def _failed(self) -> None:
        self._failures = min(100, self._failures + 1)
        if self._failures >= self._threshold:
            self._probe_at = time.monotonic() + self._interval
        self._notify()

    async def connect(self) -> _SelectedSession:
        use_sse = self._failures >= self._threshold and time.monotonic() < self._probe_at
        if not use_sse:
            try:
                session = await self._ws.connect()
            except RealtimeRecoveryRetry:
                self._failed()
                if self._failures < self._threshold:
                    raise
                use_sse = True
            except RealtimeRecoveryError as exc:
                if exc.reason != "websocket_dependency_missing":
                    raise
                self._failures = self._threshold
                self._probe_at = time.monotonic() + self._interval
                use_sse = True
        if use_sse:
            session = await self._sse.connect()
        self._transport = "sse" if use_sse else "websocket"
        self._notify()
        return _SelectedSession(self, session, use_sse)


class _SelectedSession:
    def __init__(self, owner: RealtimeFallbackClient, session, sse: bool):
        self._owner, self._session, self._sse = owner, session, sse

    async def subscribe(self, *, channel: str | None, inbox: bool, after: int) -> RealtimeRecoveryTarget:
        try:
            return await self._session.subscribe(channel=channel, inbox=inbox, after=after)
        except RealtimeRecoveryRetry:
            if not self._sse:
                self._owner._failed()
            raise

    async def replay(self, *, after: int, through: int) -> AsyncIterator[RealtimeRecoveryEvent]:
        async for event in self._session.replay(after=after, through=through):
            yield event
        if not self._sse:
            self._owner._failures = 0
            self._owner._notify()

    async def events(self) -> AsyncIterator[RealtimeRecoveryEvent]:
        async for event in self._session.events():
            yield event

    async def aclose(self) -> None:
        await self._session.aclose()
