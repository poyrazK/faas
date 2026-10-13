"""Coalesced temporary activity refresh for caller-owned authenticated transports."""

from __future__ import annotations

import asyncio
import json
import math
import re
from collections.abc import Awaitable, Callable
from typing import Any
from uuid import UUID

from . import errors
from .api.default import publish_managed_realtime_scoped_signal, publish_managed_realtime_signal
from .client import AuthenticatedClient
from .models.managed_realtime_signal_request import ManagedRealtimeSignalRequest
from .models.managed_realtime_signal_response import ManagedRealtimeSignalResponse
from .realtime_scopes import realtime_activity_scope_channel

SignalSender = Callable[[str, Any, int], Awaitable[None]]


class RealtimeActivityPublishError(Exception):
    """Activity transport failure without response bodies or transport secrets."""


class RealtimeActivityPublisher:
    """One named activity on one asyncio loop; update until clear or aclose.

    The sender must await its transport write and raise on rejection. No automatic
    transport retries or connection management are added. Keep on_error synchronous.
    """

    def __init__(
        self,
        send: SignalSender,
        *,
        name: str,
        ttl_milliseconds: int = 5000,
        interval_milliseconds: int = 250,
        idle_timeout_seconds: float | None = None,
        on_error: Callable[[RealtimeActivityPublishError], None] | None = None,
    ):
        if not callable(send) or not isinstance(name, str) or re.fullmatch(r"[A-Za-z0-9_.:-]{1,64}", name) is None:
            raise ValueError("A sender and valid named activity are required")
        if type(ttl_milliseconds) is not int or not 100 <= ttl_milliseconds <= 30000:
            raise ValueError("ttl_milliseconds must be an integer between 100 and 30000")
        if type(interval_milliseconds) is not int or not 50 <= interval_milliseconds <= ttl_milliseconds / 2:
            raise ValueError("interval_milliseconds must be at least 50 and at most half the TTL")
        if idle_timeout_seconds is not None and (
            isinstance(idle_timeout_seconds, bool)
            or not isinstance(idle_timeout_seconds, (int, float))
            or not math.isfinite(idle_timeout_seconds)
            or not 0.1 <= idle_timeout_seconds <= 86400
        ):
            raise ValueError("idle_timeout_seconds must be between 0.1 and 86400, or None")
        if on_error is not None and not callable(on_error):
            raise ValueError("on_error must be callable")
        self._loop = asyncio.get_running_loop()
        self._send, self._name, self._on_error = send, name, on_error
        self._ttl, self._interval = ttl_milliseconds, interval_milliseconds / 1000
        self._idle_timeout = idle_timeout_seconds
        self._last_update: float | None = None
        self._wake = asyncio.Event()
        self._task: asyncio.Task[None] | None = None
        self._data = "null"
        self._version = 0
        self._active = False
        self._closing = False
        self._disposed = False
        self._failure: RealtimeActivityPublishError | None = None

    def update(self, data: Any) -> None:
        """Start/refresh activity with the latest copied value, coalescing rapid updates."""
        if self._disposed or self._closing or self._failure is not None:
            raise RealtimeActivityPublishError("Activity publisher is closed, clearing, or failed")
        try:
            encoded = json.dumps(data, ensure_ascii=False, allow_nan=False, separators=(",", ":"))
            if len(encoded.encode()) > 2048:
                raise ValueError()
        except (TypeError, ValueError, OverflowError, UnicodeError, RecursionError):
            raise ValueError("Activity data must be JSON within 2048 UTF-8 bytes") from None
        self._data = encoded
        self._version += 1
        self._last_update = self._loop.time()
        self._active = True
        self._wake.set()
        if self._task is None or self._task.done():
            self._task = self._loop.create_task(self._run())

    async def _run(self) -> None:
        sent_version = -1
        last_sent: float | None = None
        try:
            while self._active:
                now = self._loop.time()
                idle_remaining = None
                if self._idle_timeout is not None and self._last_update is not None:
                    idle_remaining = self._last_update + self._idle_timeout - now
                if idle_remaining is not None and idle_remaining <= 0:
                    # Serialize expiry after prior writes. Updates arriving during this
                    # clear remain queued and restart publication after it completes.
                    version = self._version
                    async with asyncio.timeout(5):
                        await self._send(self._name, None, 0)
                    if self._version == version:
                        self._active = False
                        return
                    sent_version, last_sent = -1, None
                    continue
                delay = (
                    0.0
                    if last_sent is None
                    else max(
                        0.0, last_sent + (self._interval if sent_version != self._version else self._ttl / 2000) - now
                    )
                )
                if idle_remaining is not None:
                    delay = min(delay, idle_remaining)
                if delay:
                    self._wake.clear()
                    try:
                        await asyncio.wait_for(self._wake.wait(), timeout=delay)
                    except TimeoutError:
                        pass
                    continue
                version, encoded = self._version, self._data
                # Slow writes must not leave the owned publisher task hanging indefinitely.
                async with asyncio.timeout(5):
                    await self._send(self._name, json.loads(encoded), self._ttl)
                last_sent = self._loop.time()
                sent_version = version
        except asyncio.CancelledError:
            raise
        except Exception:
            self._active = False
            self._failure = RealtimeActivityPublishError("Temporary activity publication failed")
            if self._on_error is not None:
                try:
                    result = self._on_error(self._failure)
                    if asyncio.iscoroutine(result):
                        result.close()
                except (Exception, asyncio.CancelledError):
                    pass

    async def clear(self) -> None:
        """Stop refreshes and serialize a zero-TTL clear after the active write."""
        if self._disposed:
            raise RealtimeActivityPublishError("Activity publisher is disposed")
        if self._closing:
            raise RealtimeActivityPublishError("Activity publisher is already clearing")
        self._closing = True
        self._active = False
        self._wake.set()
        try:
            async with asyncio.timeout(5):
                if self._task is not None:
                    await self._task
                await self._send(self._name, None, 0)
        except asyncio.CancelledError:
            raise
        except Exception:
            self._failure = RealtimeActivityPublishError("Temporary activity clear failed")
            raise self._failure from None
        finally:
            await self._join()
            self._closing = False

    async def _join(self) -> None:
        if self._task is not None:
            if not self._task.done():
                self._task.cancel()
            joined = asyncio.gather(self._task, return_exceptions=True)
            interrupted = False
            while not joined.done():
                try:
                    await asyncio.shield(joined)
                except asyncio.CancelledError:
                    interrupted = True
            joined.result()
            self._task = None
            if interrupted:
                raise asyncio.CancelledError()

    async def aclose(self, *, send_clear: bool = True) -> None:
        """Dispose; optionally clear remotely, then join all refresh/write work."""
        if type(send_clear) is not bool:
            raise ValueError("send_clear must be boolean")
        if self._disposed:
            return
        try:
            if send_clear:
                await self.clear()
            else:
                self._active = False
                await self._join()
        finally:
            self._disposed = True
            self._active = False
            await self._join()

    @property
    def error(self) -> RealtimeActivityPublishError | None:
        return self._failure


def create_managed_realtime_activity_publisher(
    client: AuthenticatedClient,
    *,
    slug: str,
    endpoint_id: str,
    channel: str,
    name: str,
    activity_scope: str | None = None,
    ttl_milliseconds: int = 5000,
    interval_milliseconds: int = 250,
    idle_timeout_seconds: float | None = None,
    on_error: Callable[[RealtimeActivityPublishError], None] | None = None,
) -> RealtimeActivityPublisher:
    """Publish through HTTP as backend, never as a user presence member.

    The caller owns the client; deploy-write/MFA and optional activity-scope
    authorization apply. Acceptance is best-effort fanout, not a subscriber ACK.
    """
    target = {"slug": slug, "id": UUID(endpoint_id), "channel": channel}
    module = publish_managed_realtime_signal
    if activity_scope is not None:
        realtime_activity_scope_channel(channel, activity_scope)
        target["activity_scope"] = activity_scope
        module = publish_managed_realtime_scoped_signal

    async def send(name: str, data: Any, ttl: int) -> None:
        try:
            response = await module.asyncio_detailed(
                client=client,
                **target,
                body=ManagedRealtimeSignalRequest(data=data, name=name, ttl_ms=ttl),
            )
        except errors.UnexpectedStatus:
            raise RealtimeActivityPublishError("Backend activity request rejected") from None
        if (
            response.status_code != 200
            or not isinstance(response.parsed, ManagedRealtimeSignalResponse)
            or not response.parsed.accepted
        ):
            raise RealtimeActivityPublishError("Backend activity request rejected")

    return RealtimeActivityPublisher(
        send,
        name=name,
        ttl_milliseconds=ttl_milliseconds,
        interval_milliseconds=interval_milliseconds,
        idle_timeout_seconds=idle_timeout_seconds,
        on_error=on_error,
    )
