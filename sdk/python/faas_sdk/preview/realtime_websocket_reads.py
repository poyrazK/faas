"""Confirmed read receipts and explicit read actions for native channel/inbox sockets."""

from __future__ import annotations

import asyncio
import math
import re
from collections.abc import Callable
from dataclasses import replace
from typing import Any

from .realtime_catch_up import _sequence
from .realtime_connection_status import RealtimeConnectionStatusTracker
from .realtime_read_progress import (
    RealtimeReadProgress,
    RealtimeReadProgressTracker,
    RealtimeUnreadBadge,
    RealtimeUnreadSummary,
    _validate,
    format_realtime_unread_badge,
    summarize_realtime_unread,
)
from .realtime_recovery import RealtimeRecoveryError

_READ_ERRORS = {
    "read_progress_requires_identity",
    "invalid_read_progress",
    "read_rate_limited",
    "read_progress_unavailable",
    "read_marker_limit",
    "read_relay_unavailable",
}


class RealtimeWebSocketReadProgress:
    """One stream/principal's confirmed unread projection; never marks read on ACK.

    Use an explicit server SHA-256 reader key, not a guessed first broadcast sender.
    Callbacks are synchronous/fast. The tracker can retain other readers' receipts,
    while summary/badge always select only the configured reader.
    """

    def __init__(
        self,
        *,
        reader_id: str,
        on_change: Callable[[RealtimeUnreadSummary], None],
        on_error: Callable[[str], None] | None = None,
        max_readers: int = 1024,
        stale_after_seconds: float | None = None,
        refresh_interval_seconds: float = 1,
    ):
        if not isinstance(reader_id, str) or re.fullmatch(r"[0-9a-f]{64}", reader_id) is None:
            raise ValueError("Use the server's SHA-256 reader key")
        if not callable(on_change) or on_error is not None and not callable(on_error):
            raise ValueError("Read callbacks must be callable")
        if (
            type(refresh_interval_seconds) not in (int, float)
            or not math.isfinite(refresh_interval_seconds)
            or not 0.1 <= refresh_interval_seconds <= 60
        ):
            raise ValueError("Refresh interval must be between 0.1 and 60 seconds")
        self._interval = refresh_interval_seconds
        self._wanted = 0
        self._timer: asyncio.TimerHandle | None = None
        self._refresh_task: asyncio.Task[None] | None = None
        self._tasks: set[asyncio.Task[None]] = set()
        self._receipt = asyncio.Event()
        self.reader_id, self._on_change, self._on_error = reader_id, on_change, on_error
        self._session: Any = None
        self._stream: tuple[bool, str | None] | None = None
        self._disposed = False
        self._error: str | None = None
        self.tracker = RealtimeReadProgressTracker(
            on_change=lambda _: self._notify(), max_entries=max_readers, stale_after_seconds=stale_after_seconds
        )

    @property
    def error(self) -> str | None:
        return self._error

    def _bind(self, session: Any, channel: str | None, inbox: bool) -> None:
        if self._disposed or self._session is not None and self._session is not session:
            raise RealtimeRecoveryError("read_progress_already_bound_or_disposed")
        stream = inbox, channel
        if self._stream is not None and self._stream != stream:
            raise RealtimeRecoveryError("read_progress_stream_changed")
        self._session, self._stream = session, stream
        self._error = None
        self._notify()

    def summary(self) -> RealtimeUnreadSummary | None:
        if self._stream is None:
            return None
        inbox, channel = self._stream
        return summarize_realtime_unread(
            self.tracker.snapshot(),
            reader_id=self.reader_id,
            channels=() if inbox else (channel,),
            include_inbox=inbox,
            freshness=tuple(
                replace(entry, stale=True)
                if entry.progress.reader_id == self.reader_id and entry.progress.latest_sequence < self._wanted
                else entry
                for entry in self.tracker.freshness_snapshot()
            ),
        )

    def badge(self, *, max_count: int = 99, show_zero: bool = False) -> RealtimeUnreadBadge | None:
        summary = self.summary()
        return format_realtime_unread_badge(summary, max_count=max_count, show_zero=show_zero) if summary else None

    def _notify(self) -> None:
        summary = self.summary()
        if summary is not None:
            RealtimeConnectionStatusTracker._callback(self._on_change, summary)

    def _invalidate(self, session: Any) -> None:
        if self._session is not session:
            return
        self._session = None
        self._cancel_refresh()
        self.tracker.mark_stale()

    def dispose(self) -> None:
        self._disposed = True
        self._session = None
        self._cancel_refresh()
        self.tracker.dispose()

    def _receive(self, session: Any, frame: dict) -> None:
        if self._disposed or self._session is not session:
            return
        inbox, channel = self._stream
        flag = frame.get("inbox", False)
        if type(flag) is not bool or flag != inbox or not inbox and frame.get("channel") != channel:
            raise ValueError("Invalid read receipt scope")
        progress = RealtimeReadProgress(
            reader_id=frame.get("member_id"),
            sequence=frame.get("sequence", 0),
            unread=frame.get("unread", 0),
            oldest_sequence=frame.get("oldest_sequence", 0),
            latest_sequence=frame.get("latest_sequence", 0),
            history_unavailable=frame.get("history_unavailable", False),
            inbox=inbox,
            channel=channel,
        )
        _validate(progress)
        previous = next((p for p in self.tracker.snapshot() if p.reader_id == self.reader_id), None)
        own_valid = progress.reader_id == self.reader_id and (
            previous is None
            or (
                progress.sequence >= previous.sequence
                and progress.oldest_sequence >= previous.oldest_sequence
                and progress.latest_sequence >= previous.latest_sequence
            )
        )
        if own_valid:
            self._error = None
        self.tracker.apply(progress)
        if own_valid:
            self._receipt.set()

    def _rejected(self, session: Any, code: Any) -> None:
        if self._session is not session or self._disposed:
            return
        safe = code if isinstance(code, str) and code in _READ_ERRORS else "read_progress_failed"
        self._error = safe
        self._receipt.set()
        self.tracker.mark_stale()
        if self._on_error is not None:
            RealtimeConnectionStatusTracker._callback(self._on_error, safe)

    def _cancel_refresh(self) -> None:
        if self._timer is not None:
            self._timer.cancel()
            self._timer = None
        if self._refresh_task is not None:
            self._refresh_task.cancel()

    async def _join_refresh(self) -> None:
        if self._tasks:
            await asyncio.gather(*tuple(self._tasks), return_exceptions=True)

    def _new_position(self, session: Any, sequence: int) -> None:
        if self._disposed or self._session is not session:
            return
        self._wanted = max(self._wanted, sequence)
        own = next((p for p in self.tracker.snapshot() if p.reader_id == self.reader_id), None)
        if own is not None and own.latest_sequence >= self._wanted:
            return
        self._notify()
        self._schedule_refresh()

    def _schedule_refresh(self) -> None:
        if self._session is not None and self._timer is None and self._refresh_task is None and not self._disposed:
            self._timer = asyncio.get_running_loop().call_later(self._interval, self._start_refresh)

    def _start_refresh(self) -> None:
        self._timer = None
        session = self._session
        if session is None or self._disposed:
            return

        async def query() -> None:
            try:
                self._receipt.clear()
                async with asyncio.timeout(5):
                    await self.refresh()
                    await self._receipt.wait()
            except asyncio.CancelledError:
                raise
            except Exception:
                self._rejected(session, "read_progress_unavailable")
            finally:
                if self._refresh_task is asyncio.current_task():
                    self._refresh_task = None
                own = next((p for p in self.tracker.snapshot() if p.reader_id == self.reader_id), None)
                if (
                    self._session is session
                    and self._error is None
                    and own is not None
                    and own.latest_sequence < self._wanted
                ):
                    self._schedule_refresh()

        task = asyncio.create_task(query())
        self._refresh_task = task
        self._tasks.add(task)
        task.add_done_callback(self._tasks.discard)

    def _current(self) -> Any:
        session = self._session
        if self._disposed or session is None or session._closed or not session._active or session._failure is not None:
            raise RealtimeRecoveryError("read_progress_not_connected")
        return session

    async def mark_read(self, sequence: int) -> None:
        """Assert the person saw messages through this completed stream position.

        Returns after local socket write, not server receipt. Filter checkpoints
        are whole-channel positions: consider skipped events before asserting read.
        """
        _sequence(sequence)
        session = self._current()
        if sequence > session._acked:
            raise ValueError("Cannot mark read beyond completed processing/ACK progress")
        inbox, channel = self._stream
        await session._send(
            {"type": "inbox_read", "sequence": sequence}
            if inbox
            else {"type": "read", "channel": channel, "sequence": sequence}
        )

    async def refresh(self) -> None:
        """Query current counts without changing read state; no automatic retries."""
        session = self._current()
        inbox, channel = self._stream
        await session._send({"type": "inbox_read_state"} if inbox else {"type": "read_state", "channel": channel})
