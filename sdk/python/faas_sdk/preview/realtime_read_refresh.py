"""Bounded read-state refresh through a caller-owned authenticated transport."""

from __future__ import annotations

import asyncio
import math
from collections.abc import Awaitable, Callable

from .realtime_read_progress import RealtimeReadProgress, RealtimeReadProgressTracker, _validate


class RealtimeReadRefreshError(Exception):
    """Refresh failure without raw transport, response or credential details."""


class RealtimeReadRefreshCoordinator:
    """Use on one asyncio loop; bind tracker and fetcher to one endpoint/account.

    fetch returns a confirmed receipt for the requested reader/stream. It must
    cooperate with cancellation. No retries or server read writes are introduced.
    """

    def __init__(
        self,
        tracker: RealtimeReadProgressTracker,
        fetch: Callable[[RealtimeReadProgress], Awaitable[RealtimeReadProgress]],
        *,
        concurrency: int = 4,
        max_pending: int = 1024,
        timeout_seconds: float = 5,
        on_error: Callable[[RealtimeReadProgress, RealtimeReadRefreshError], None] | None = None,
    ):
        if not isinstance(tracker, RealtimeReadProgressTracker) or not callable(fetch):
            raise ValueError("A read-progress tracker and fetch callback are required")
        for name, value, high in (("concurrency", concurrency, 32), ("max_pending", max_pending, 8192)):
            if type(value) is not int or not 1 <= value <= high:
                raise ValueError(f"{name} must be an integer between 1 and {high}")
        if (
            isinstance(timeout_seconds, bool)
            or not isinstance(timeout_seconds, (int, float))
            or not math.isfinite(timeout_seconds)
            or not 0.1 <= timeout_seconds <= 60
        ):
            raise ValueError("timeout_seconds must be between 0.1 and 60")
        if on_error is not None and not callable(on_error):
            raise ValueError("on_error must be callable")
        self._loop = asyncio.get_running_loop()
        self._tracker, self._fetch, self._on_error = tracker, fetch, on_error
        self._concurrency, self._capacity, self._timeout = concurrency, max_pending, timeout_seconds
        self._queued: dict[tuple[bool, str | None, str], tuple[RealtimeReadProgress, int]] = {}
        self._active: dict[tuple[bool, str | None, str], asyncio.Task[None]] = {}
        self._closed = False

    @staticmethod
    def _key(progress: RealtimeReadProgress) -> tuple[bool, str | None, str]:
        return progress.inbox, progress.channel, progress.reader_id

    def request(self, receipts: tuple[RealtimeReadProgress, ...]) -> int:
        """Enqueue distinct reader/streams atomically; queued duplicates keep the latest input.

        Requests already in flight coalesce without creating another request.
        Returns the number of newly admitted identities.
        """
        if self._closed:
            raise RealtimeReadRefreshError("Read refresh coordinator is closed")
        if not isinstance(receipts, (tuple, list)) or len(receipts) > 8192:
            raise ValueError("Refresh requests must contain at most 8192 receipts")
        incoming = {}
        for receipt in receipts:
            _validate(receipt)
            incoming[self._key(receipt)] = receipt
        new = incoming.keys() - self._queued.keys() - self._active.keys()
        if len(self._queued) + len(self._active) + len(new) > self._capacity:
            raise RealtimeReadRefreshError("Read refresh capacity reached")
        for key, receipt in incoming.items():
            if key not in self._active:
                self._queued[key] = receipt, self._tracker._generation
        self._start()
        return len(new)

    def _start(self) -> None:
        while not self._closed and self._queued and len(self._active) < self._concurrency:
            key = next(iter(self._queued))
            progress, generation = self._queued.pop(key)
            task = self._loop.create_task(self._refresh(progress, generation))
            self._active[key] = task
            task.add_done_callback(lambda completed, key=key: self._finished(key, completed))

    def _finished(self, key: tuple[bool, str | None, str], task: asyncio.Task[None]) -> None:
        self._active.pop(key, None)
        if not task.cancelled():
            task.exception()
        self._start()

    async def _refresh(self, progress: RealtimeReadProgress, generation: int) -> None:
        if generation != self._tracker._generation or self._tracker._disposed:
            return
        try:
            async with asyncio.timeout(self._timeout):
                response = await self._fetch(progress)
            if self._closed or generation != self._tracker._generation or self._tracker._disposed:
                return
            _validate(response)
            if self._key(response) != self._key(progress):
                raise ValueError("Refresh identity mismatch")
            if (
                response.sequence < progress.sequence
                or response.latest_sequence < progress.latest_sequence
                or response.oldest_sequence < progress.oldest_sequence
            ):
                raise ValueError("Refresh bounds moved backward")
            self._tracker.apply(response)
        except asyncio.CancelledError:
            if not self._closed:
                self._report(progress)
            raise
        except Exception:
            self._report(progress)

    def _report(self, progress: RealtimeReadProgress) -> None:
        if self._on_error is not None:
            try:
                result = self._on_error(progress, RealtimeReadRefreshError("Read progress refresh failed"))
                if asyncio.iscoroutine(result):
                    result.close()
            except (Exception, asyncio.CancelledError):
                pass

    async def wait_idle(self) -> None:
        """Await current queued/in-flight work; cancellation leaves the coordinator running."""
        while self._active:
            await asyncio.shield(asyncio.gather(*tuple(self._active.values()), return_exceptions=True))
            # Allow done callbacks to admit queued work before checking again.
            await asyncio.sleep(0)

    async def aclose(self) -> None:
        """Discard queued work, cancel requests and join owned tasks before transport closure."""
        self._closed = True
        self._queued.clear()
        tasks = tuple(self._active.values())
        for task in tasks:
            task.cancel()
        joined = asyncio.gather(*tasks, return_exceptions=True)
        interrupted = False
        while not joined.done():
            try:
                await asyncio.shield(joined)
            except asyncio.CancelledError:
                interrupted = True
        joined.result()
        if interrupted:
            raise asyncio.CancelledError()

    @property
    def pending_count(self) -> int:
        return len(self._queued) + len(self._active)
