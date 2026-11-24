"""Local monotonic views of confirmed server read receipts; never advances read state."""

from __future__ import annotations

import asyncio
import math
import re
import time
from collections.abc import Callable
from dataclasses import dataclass

_MAX_SEQUENCE = 2**63 - 1


@dataclass(frozen=True, kw_only=True)
class RealtimeReadProgress:
    reader_id: str
    sequence: int
    unread: int
    oldest_sequence: int
    latest_sequence: int
    history_unavailable: bool = False
    inbox: bool = False
    channel: str | None = None


def _validate(progress: RealtimeReadProgress) -> None:
    if not isinstance(progress, RealtimeReadProgress):
        raise ValueError("Expected RealtimeReadProgress")
    if not isinstance(progress.reader_id, str) or re.fullmatch(r"[0-9a-f]{64}", progress.reader_id) is None:
        raise ValueError("reader_id must be the server's SHA-256 principal key")
    if type(progress.inbox) is not bool or type(progress.history_unavailable) is not bool:
        raise ValueError("Read-progress flags must be booleans")
    if progress.inbox:
        if progress.channel is not None:
            raise ValueError("Inbox read progress must not have a channel")
    elif not isinstance(progress.channel, str) or not progress.channel or len(progress.channel.encode()) > 256:
        raise ValueError("Channel read progress requires a channel within 256 UTF-8 bytes")
    for value in (progress.sequence, progress.unread, progress.oldest_sequence, progress.latest_sequence):
        if type(value) is not int or not 0 <= value <= _MAX_SEQUENCE:
            raise ValueError("Read-progress counts must be nonnegative signed 64-bit integers")
    if (
        progress.sequence > progress.latest_sequence
        or not 1 <= progress.oldest_sequence <= progress.latest_sequence + 1
    ):
        raise ValueError("Invalid read-progress sequence bounds")


@dataclass(frozen=True, kw_only=True)
class RealtimeReadProgressFreshness:
    progress: RealtimeReadProgress
    age_seconds: float
    stale: bool


class RealtimeReadProgressTracker:
    """Bounded synchronous tracker; feed authenticated receipts, reset on reconnect.

    Use from one execution context. Callback exceptions do not change confirmed
    state. The tracker owns no transport, timers or durable storage.
    """

    def __init__(
        self,
        *,
        on_change: Callable[[tuple[RealtimeReadProgress, ...]], None],
        max_entries: int = 1024,
        stale_after_seconds: float | None = None,
        on_refresh: Callable[[tuple[RealtimeReadProgress, ...]], None] | None = None,
    ):
        if type(max_entries) is not int or not 1 <= max_entries <= 8192:
            raise ValueError("max_entries must be an integer between 1 and 8192")
        if not callable(on_change):
            raise ValueError("on_change must be callable")
        if stale_after_seconds is not None and (
            isinstance(stale_after_seconds, bool)
            or not isinstance(stale_after_seconds, (int, float))
            or not math.isfinite(stale_after_seconds)
            or not 0.1 <= stale_after_seconds <= 86400
        ):
            raise ValueError("stale_after_seconds must be between 0.1 and 86400, or None")
        if on_refresh is not None and not callable(on_refresh):
            raise ValueError("on_refresh must be callable")
        self._stale_after = stale_after_seconds
        self._on_refresh = on_refresh
        self._received: dict[tuple[bool, str | None, str], float] = {}
        self._forced_stale: set[tuple[bool, str | None, str]] = set()
        self._on_change = on_change
        self._max_entries = max_entries
        self._entries: dict[tuple[bool, str | None, str], RealtimeReadProgress] = {}
        self._disposed = False
        self._generation = 0

    def _notify(self) -> None:
        try:
            result = self._on_change(self.snapshot())
            if asyncio.iscoroutine(result):
                result.close()
        except (Exception, asyncio.CancelledError):
            pass

    def apply(self, progress: RealtimeReadProgress) -> bool:
        """Accept confirmed progress; ignore exact duplicates or backward sequence bounds."""
        if self._disposed:
            raise ValueError("Read-progress tracker is disposed")
        _validate(progress)
        key = (progress.inbox, progress.channel, progress.reader_id)
        previous = self._entries.get(key)
        if previous is not None and (
            progress.sequence < previous.sequence
            or progress.latest_sequence < previous.latest_sequence
            or progress.oldest_sequence < previous.oldest_sequence
        ):
            return False
        if previous is None and len(self._entries) >= self._max_entries:
            raise ValueError("Read-progress tracker capacity reached")
        was_stale = key in self._forced_stale or (
            key in self._received
            and self._stale_after is not None
            and time.monotonic() - self._received[key] >= self._stale_after
        )
        self._received[key] = time.monotonic()
        self._forced_stale.discard(key)
        if progress == previous:
            if was_stale:
                self._notify()
            return False
        self._entries[key] = progress
        self._notify()
        return True

    def snapshot(self, *, channel: str | None = None, inbox: bool | None = None) -> tuple[RealtimeReadProgress, ...]:
        """Return sorted immutable receipts; inboxes and channels remain separate streams."""
        self._validate_filter(channel, inbox)
        return tuple(
            sorted(
                (
                    progress
                    for progress in self._entries.values()
                    if (channel is None or progress.channel == channel) and (inbox is None or progress.inbox == inbox)
                ),
                key=lambda progress: (progress.inbox, progress.channel or "", progress.reader_id),
            )
        )

    def freshness_snapshot(
        self,
        *,
        channel: str | None = None,
        inbox: bool | None = None,
    ) -> tuple[RealtimeReadProgressFreshness, ...]:
        now = time.monotonic()
        result = []
        for progress in self.snapshot(channel=channel, inbox=inbox):
            key = (progress.inbox, progress.channel, progress.reader_id)
            age = max(0.0, now - self._received[key])
            result.append(
                RealtimeReadProgressFreshness(
                    progress=progress,
                    age_seconds=age,
                    stale=key in self._forced_stale or self._stale_after is not None and age >= self._stale_after,
                )
            )
        return tuple(result)

    def mark_stale(self, *, channel: str | None = None, inbox: bool | None = None) -> None:
        """Keep last confirmed counts but mark matching streams stale on transport loss."""
        changed = False
        for progress in self.snapshot(channel=channel, inbox=inbox):
            key = (progress.inbox, progress.channel, progress.reader_id)
            if key not in self._forced_stale:
                self._forced_stale.add(key)
                changed = True
        if changed:
            self._notify()

    def refresh_stale(
        self, *, channel: str | None = None, inbox: bool | None = None
    ) -> tuple[RealtimeReadProgress, ...]:
        """Request refresh via the optional synchronous callback; never mark data fresh."""
        stale = tuple(item.progress for item in self.freshness_snapshot(channel=channel, inbox=inbox) if item.stale)
        if stale and self._on_refresh is not None:
            try:
                result = self._on_refresh(stale)
                if asyncio.iscoroutine(result):
                    result.close()
            except (Exception, asyncio.CancelledError):
                pass
        return stale

    @staticmethod
    def _validate_filter(channel: str | None, inbox: bool | None) -> None:
        if inbox is not None and type(inbox) is not bool:
            raise ValueError("inbox filter must be boolean or None")
        if channel is not None and (
            not isinstance(channel, str) or not channel or len(channel.encode()) > 256 or inbox is True
        ):
            raise ValueError("Invalid channel/inbox filter")

    def reset(self, *, channel: str | None = None, inbox: bool | None = None) -> None:
        """Forget local ordering/state for reconnect or an authorized stream replacement."""
        self._validate_filter(channel, inbox)
        if self._disposed:
            return
        self._generation += 1
        keys = [
            key
            for key, progress in self._entries.items()
            if (channel is None or progress.channel == channel) and (inbox is None or progress.inbox == inbox)
        ]
        for key in keys:
            del self._entries[key]
            self._received.pop(key, None)
            self._forced_stale.discard(key)
        if keys:
            self._notify()

    def dispose(self) -> None:
        if self._disposed:
            return
        changed = bool(self._entries)
        self._disposed = True
        self._generation += 1
        self._entries.clear()
        self._received.clear()
        self._forced_stale.clear()
        if changed:
            self._notify()


@dataclass(frozen=True, kw_only=True)
class RealtimeUnreadSummary:
    reader_id: str
    streams: tuple[RealtimeReadProgress, ...]
    missing_channels: tuple[str, ...]
    inbox_missing: bool
    channels_explicit: bool
    stale_streams: tuple[RealtimeReadProgress, ...] | None = None

    @property
    def total_unread(self) -> int:
        return sum(stream.unread for stream in self.streams)

    @property
    def unread_stream_count(self) -> int:
        return sum(stream.unread > 0 for stream in self.streams)

    @property
    def history_gaps(self) -> tuple[RealtimeReadProgress, ...]:
        return tuple(stream for stream in self.streams if stream.history_unavailable)

    @property
    def history_unavailable(self) -> bool:
        return bool(self.history_gaps)

    @property
    def complete(self) -> bool | None:
        """Requested streams are present; None when channel coverage was not specified.

        This says nothing about receipt freshness or messages removed by retention.
        """
        if not self.channels_explicit:
            return None
        return not self.missing_channels and not self.inbox_missing


def summarize_realtime_unread(
    receipts: tuple[RealtimeReadProgress, ...],
    *,
    reader_id: str,
    channels: tuple[str, ...] | None = None,
    include_inbox: bool = True,
    freshness: tuple[RealtimeReadProgressFreshness, ...] | None = None,
) -> RealtimeUnreadSummary:
    """Sum confirmed counts for one reader; no guesses for missing/unloaded streams.

    Counts are per stream, not globally deduplicated across channels and inboxes.
    Only pass receipts belonging to the same endpoint, because stream names alone
    do not distinguish endpoints.
    """
    if not isinstance(reader_id, str) or re.fullmatch(r"[0-9a-f]{64}", reader_id) is None:
        raise ValueError("reader_id must be the server's SHA-256 principal key")
    if type(include_inbox) is not bool:
        raise ValueError("include_inbox must be boolean")
    if not isinstance(receipts, (tuple, list)) or len(receipts) > 8192:
        raise ValueError("receipts must contain at most 8192 read-progress entries")
    selected: set[str] | None = None
    if channels is not None:
        if not isinstance(channels, (tuple, list)) or len(channels) > 8192:
            raise ValueError("channels must contain at most 8192 channel names")
        for channel in channels:
            RealtimeReadProgressTracker._validate_filter(channel, False)
            if channel is None:
                raise ValueError("Channel names must be strings")
        selected = set(channels)
        if len(selected) != len(channels):
            raise ValueError("Duplicate selected channel")
    streams = []
    seen: set[tuple[bool, str | None, str]] = set()
    for receipt in receipts:
        _validate(receipt)
        key = (receipt.inbox, receipt.channel, receipt.reader_id)
        if key in seen:
            raise ValueError("Duplicate read-progress stream/reader")
        seen.add(key)
        if receipt.reader_id != reader_id:
            continue
        if receipt.inbox:
            if include_inbox:
                streams.append(receipt)
        elif selected is None or receipt.channel in selected:
            streams.append(receipt)
    stale_streams = None
    if freshness is not None:
        if not isinstance(freshness, (tuple, list)) or len(freshness) > 8192:
            raise ValueError("Invalid freshness snapshot")
        by_key = {}
        for item in freshness:
            if (
                not isinstance(item, RealtimeReadProgressFreshness)
                or type(item.stale) is not bool
                or isinstance(item.age_seconds, bool)
                or not isinstance(item.age_seconds, (int, float))
                or not math.isfinite(item.age_seconds)
                or item.age_seconds < 0
            ):
                raise ValueError("Invalid freshness entry")
            _validate(item.progress)
            key = (item.progress.inbox, item.progress.channel, item.progress.reader_id)
            if key in by_key:
                raise ValueError("Duplicate freshness entry")
            by_key[key] = item
        stale_streams = []
        for stream in streams:
            item = by_key.get((stream.inbox, stream.channel, stream.reader_id))
            if item is None or item.progress != stream or item.stale:
                stale_streams.append(stream)
        stale_streams = tuple(sorted(stale_streams, key=lambda stream: (stream.inbox, stream.channel or "")))
    streams.sort(key=lambda receipt: (receipt.inbox, receipt.channel or ""))
    known_channels = {stream.channel for stream in streams if not stream.inbox}
    return RealtimeUnreadSummary(
        reader_id=reader_id,
        streams=tuple(streams),
        missing_channels=tuple(sorted(selected - known_channels)) if selected is not None else (),
        inbox_missing=include_inbox and not any(stream.inbox for stream in streams),
        channels_explicit=selected is not None,
        stale_streams=stale_streams,
    )


@dataclass(frozen=True, kw_only=True)
class RealtimeUnreadBadge:
    text: str
    count_text: str
    accessible_label: str
    total_unread: int
    capped: bool
    updating: bool
    history_unavailable: bool
    visible: bool


def format_realtime_unread_badge(
    summary: RealtimeUnreadSummary,
    *,
    max_count: int = 99,
    show_zero: bool = False,
) -> RealtimeUnreadBadge:
    """English presentation for confirmed totals, with uncertainty kept explicit.

    Updating is a display state, not a promise that a refresh request is running.
    count_text always describes the known total; text hides it when unverified.
    """
    if not isinstance(summary, RealtimeUnreadSummary):
        raise ValueError("Expected RealtimeUnreadSummary")
    if type(max_count) is not int or not 1 <= max_count <= 999999:
        raise ValueError("max_count must be an integer between 1 and 999999")
    if type(show_zero) is not bool:
        raise ValueError("show_zero must be boolean")
    if (
        type(summary.inbox_missing) is not bool
        or type(summary.channels_explicit) is not bool
        or not isinstance(summary.streams, (tuple, list))
        or len(summary.streams) > 8192
        or not isinstance(summary.missing_channels, (tuple, list))
        or len(summary.missing_channels) > 8192
    ):
        raise ValueError("Invalid unread summary")
    if not isinstance(summary.reader_id, str) or re.fullmatch(r"[0-9a-f]{64}", summary.reader_id) is None:
        raise ValueError("Invalid summary reader ID")
    seen = set()
    for stream in summary.streams:
        _validate(stream)
        key = (stream.inbox, stream.channel)
        if stream.reader_id != summary.reader_id or key in seen:
            raise ValueError("Invalid or duplicate summary stream")
        seen.add(key)
    for channel in summary.missing_channels:
        if channel is None:
            raise ValueError("Invalid missing channel")
        RealtimeReadProgressTracker._validate_filter(channel, False)
    if summary.stale_streams is not None:
        if not isinstance(summary.stale_streams, (tuple, list)) or len(summary.stale_streams) > 8192:
            raise ValueError("Invalid stale streams")
        for stream in summary.stale_streams:
            _validate(stream)
            if stream not in summary.streams:
                raise ValueError("Stale stream must belong to the summary")
    total = summary.total_unread
    capped = total > max_count
    count_text = f"{max_count}+" if capped else str(total)
    updating = summary.complete is not True or summary.stale_streams is None or bool(summary.stale_streams)
    gap = summary.history_unavailable
    text = "Updating…" if updating else count_text if total or show_zero or gap else ""
    count_label = f"{total} unread {'message' if total == 1 else 'messages'}"
    accessible_label = f"Unread count updating. Known count: {count_label}." if updating else f"{count_label}."
    if gap:
        accessible_label += " Earlier history is unavailable; counts cover retained messages."
    return RealtimeUnreadBadge(
        text=text,
        count_text=count_text,
        accessible_label=accessible_label,
        total_unread=total,
        capped=capped,
        updating=updating,
        history_unavailable=gap,
        visible=bool(text) or gap,
    )
