"""Coalesced explicit read assertions; pending positions are never confirmed receipts."""

from __future__ import annotations

import asyncio
from collections.abc import Awaitable, Callable
from uuid import UUID

from .api.realtime import advance_managed_realtime_channel_read_progress, advance_managed_realtime_inbox_read_progress
from .client import AuthenticatedClient
from .models.managed_realtime_read_progress_request import ManagedRealtimeReadProgressRequest
from .models.managed_realtime_read_progress_response import ManagedRealtimeReadProgressResponse


class RealtimeReadPublishError(Exception):
    """Read assertion transport failure without sensitive response or request details."""


class RealtimeReadProgressPublisher:
    """Bind one instance to one reader/stream on one asyncio loop.

    send must await its write and raise on failure. Calling mark_read asserts that
    the person actually viewed messages; receiving a message is not sufficient.
    """

    def __init__(
        self,
        send: Callable[[int], Awaitable[None]],
        *,
        interval_milliseconds: int = 250,
        on_error: Callable[[RealtimeReadPublishError], None] | None = None,
    ):
        if not callable(send):
            raise ValueError("send must be callable")
        if type(interval_milliseconds) is not int or not 50 <= interval_milliseconds <= 30000:
            raise ValueError("interval_milliseconds must be an integer between 50 and 30000")
        if on_error is not None and not callable(on_error):
            raise ValueError("on_error must be callable")
        self._loop = asyncio.get_running_loop()
        self._send, self._on_error = send, on_error
        self._interval = interval_milliseconds / 1000
        self._wake = asyncio.Event()
        self._task: asyncio.Task[None] | None = None
        self._pending: int | None = None
        self._highest = 0
        self._last_sent: float | None = None
        self._flushing = 0
        self._disposed = False
        self._failure: RealtimeReadPublishError | None = None

    def mark_read(self, sequence: int) -> bool:
        if self._disposed or self._failure is not None:
            raise RealtimeReadPublishError("Read publisher is closed or failed")
        if type(sequence) is not int or not 0 <= sequence <= 2**63 - 1:
            raise ValueError("sequence must be a nonnegative signed 64-bit integer")
        if sequence <= self._highest:
            return False
        self._highest = sequence
        self._pending = sequence
        self._wake.set()
        if self._task is None or self._task.done():
            self._task = self._loop.create_task(self._run())
        return True

    async def _run(self) -> None:
        try:
            while self._pending is not None:
                delay = (
                    0.0
                    if self._flushing or self._last_sent is None
                    else max(
                        0.0,
                        self._last_sent + self._interval - self._loop.time(),
                    )
                )
                if delay:
                    self._wake.clear()
                    try:
                        await asyncio.wait_for(self._wake.wait(), timeout=delay)
                    except TimeoutError:
                        pass
                    continue
                sequence = self._pending
                async with asyncio.timeout(5):
                    await self._send(sequence)
                self._last_sent = self._loop.time()
                if self._pending == sequence:
                    self._pending = None
        except asyncio.CancelledError:
            self._failure = RealtimeReadPublishError("Read progress publication interrupted")
            raise
        except Exception:
            self._failure = RealtimeReadPublishError("Read progress publication failed")
            if self._on_error is not None:
                try:
                    result = self._on_error(self._failure)
                    if asyncio.iscoroutine(result):
                        result.close()
                except (Exception, asyncio.CancelledError):
                    pass

    async def flush(self) -> None:
        """Bypass throttle and await owned writes under a five-second total budget."""
        self._flushing += 1
        self._wake.set()
        try:
            async with asyncio.timeout(5):
                if self._task is not None:
                    await self._task
            if self._failure is not None:
                raise self._failure
        except asyncio.CancelledError:
            await self._cancel_join()
            self._failure = RealtimeReadPublishError("Read progress flush interrupted")
            raise
        except TimeoutError:
            await self._cancel_join()
            self._failure = RealtimeReadPublishError("Read progress flush timed out")
            raise self._failure from None
        finally:
            self._flushing -= 1

    async def _cancel_join(self) -> None:
        if self._task is None:
            return
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

    async def aclose(self, *, flush: bool = True) -> None:
        if type(flush) is not bool:
            raise ValueError("flush must be boolean")
        if self._disposed:
            return
        self._disposed = True
        try:
            if flush:
                await self.flush()
        finally:
            await self._cancel_join()

    @property
    def pending_sequence(self) -> int | None:
        return self._pending

    @property
    def error(self) -> RealtimeReadPublishError | None:
        return self._failure


def create_managed_realtime_read_publisher(
    client: AuthenticatedClient,
    *,
    slug: str,
    endpoint_id: str,
    principal: str,
    channel: str | None = None,
    interval_milliseconds: int = 250,
    on_error: Callable[[RealtimeReadPublishError], None] | None = None,
) -> RealtimeReadProgressPublisher:
    """Account HTTP assertions for one principal; channel=None targets their inbox.

    The caller owns the client. Existing deployment-write/MFA checks apply.
    Confirmed badges remain driven by authoritative read-progress responses/receipts.
    """
    if not isinstance(principal, str) or not principal:
        raise ValueError("principal is required")
    target = {"slug": slug, "id": UUID(endpoint_id), "principal": principal}
    module = advance_managed_realtime_inbox_read_progress
    if channel is not None:
        if not isinstance(channel, str) or not channel or len(channel.encode()) > 256:
            raise ValueError("Invalid channel")
        target["channel"] = channel
        module = advance_managed_realtime_channel_read_progress

    async def send(sequence: int) -> None:
        response = await module.asyncio_detailed(
            client=client,
            **target,
            body=ManagedRealtimeReadProgressRequest(sequence=sequence),
        )
        if (
            response.status_code != 200
            or not isinstance(response.parsed, ManagedRealtimeReadProgressResponse)
            or type(response.parsed.sequence) is not int
            or response.parsed.sequence < sequence
        ):
            raise RealtimeReadPublishError("Read progress request rejected")

    return RealtimeReadProgressPublisher(send, interval_milliseconds=interval_milliseconds, on_error=on_error)
