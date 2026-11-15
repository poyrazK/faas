"""Bounded, expiring named activity for signals received by an application's realtime transport."""

from __future__ import annotations

import asyncio
import calendar
import json
import re
import time
from collections.abc import Callable
from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Any

_WINDOW_NS = 30_000_000_000
_TIMESTAMP = re.compile(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.(\d{1,9}))?(?:Z|[+-]\d{2}:\d{2})")


@dataclass(frozen=True, kw_only=True)
class RealtimeActivity:
    channel: str
    member_id: str
    name: str
    updated_at: str
    expires_at: str
    data: Any


def _timestamp(value: str) -> int:
    if not isinstance(value, str) or (match := _TIMESTAMP.fullmatch(value)) is None:
        raise ValueError("Activity timestamps must be RFC3339")
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00")).astimezone(timezone.utc)
    except ValueError:
        raise ValueError("Activity timestamps must be RFC3339") from None
    return calendar.timegm(parsed.replace(microsecond=0).timetuple()) * 1_000_000_000 + int(
        (match.group(1) or "").ljust(9, "0")
    )


def _copy(activity: RealtimeActivity) -> RealtimeActivity:
    return RealtimeActivity(
        channel=activity.channel,
        member_id=activity.member_id,
        name=activity.name,
        updated_at=activity.updated_at,
        expires_at=activity.expires_at,
        data=json.loads(json.dumps(activity.data)),
    )


@dataclass
class _Entry:
    activity: RealtimeActivity | None
    revision: int
    expires: int
    forget_at: int


class RealtimeActivityTracker:
    """Use on one running asyncio loop; callbacks must return quickly and synchronously.

    Feed authenticated named signal frames through apply, remove members on presence
    departure, and reset on reconnect. The tracker creates no connection or subscription.
    """

    def __init__(self, *, on_change: Callable[[list[RealtimeActivity]], None], max_entries: int = 1024):
        if type(max_entries) is not int or not 1 <= max_entries <= 8192:
            raise ValueError("max_entries must be an integer between 1 and 8192")
        if not callable(on_change):
            raise ValueError("on_change must be callable")
        self._loop = asyncio.get_running_loop()
        self._on_change = on_change
        self._max_entries = max_entries
        self._entries: dict[tuple[str, str, str], _Entry] = {}
        self._timer: asyncio.TimerHandle | None = None
        self._disposed = False

    def _prune(self, now: int) -> bool:
        changed = False
        for key, entry in tuple(self._entries.items()):
            if entry.activity is not None and entry.expires <= now:
                entry.activity = None
                changed = True
            if entry.activity is None and entry.forget_at <= now:
                del self._entries[key]
        return changed

    def _current(self) -> list[RealtimeActivity]:
        return [_copy(entry.activity) for entry in self._entries.values() if entry.activity is not None]

    def _notify(self) -> None:
        try:
            result = self._on_change(self._current())
            # Asynchronous callbacks are unsupported; close accidental coroutine results.
            if asyncio.iscoroutine(result):
                result.close()
        except (Exception, asyncio.CancelledError):
            pass

    def _schedule(self) -> None:
        if self._timer is not None:
            self._timer.cancel()
            self._timer = None
        if self._disposed or not self._entries:
            return
        next_expiry = min(
            entry.expires if entry.activity is not None else entry.forget_at for entry in self._entries.values()
        )
        self._timer = self._loop.call_later(max(0.001, (next_expiry - time.time_ns()) / 1e9), self._expire)

    def _expire(self) -> None:
        self._timer = None
        changed = self._prune(time.time_ns())
        self._schedule()
        if changed:
            self._notify()

    def apply(self, activity: RealtimeActivity) -> bool:
        """Accept a newer named signal; duplicate/stale signals return False. Zero TTL clears."""
        if self._disposed:
            raise ValueError("Activity tracker is disposed")
        if not isinstance(activity, RealtimeActivity):
            raise ValueError("Expected RealtimeActivity")
        if (
            not isinstance(activity.channel, str)
            or not activity.channel
            or len(activity.channel.encode()) > 256
            or not isinstance(activity.member_id, str)
            or not 1 <= len(activity.member_id) <= 128
            or not isinstance(activity.name, str)
            or re.fullmatch(r"[A-Za-z0-9_.:-]{1,64}", activity.name) is None
        ):
            raise ValueError("Invalid named activity identity")
        updated, expires = _timestamp(activity.updated_at), _timestamp(activity.expires_at)
        if not 0 <= expires - updated <= _WINDOW_NS:
            raise ValueError("Activity TTL must be between 0 and 30000 milliseconds")
        now = time.time_ns()
        if not now - _WINDOW_NS <= updated <= now + _WINDOW_NS:
            return False
        try:
            encoded = json.dumps(activity.data, ensure_ascii=False, allow_nan=False, separators=(",", ":"))
            if len(encoded.encode()) > 2048:
                raise ValueError()
            isolated = json.loads(encoded)
        except (TypeError, ValueError, OverflowError, UnicodeError, RecursionError):
            raise ValueError("Activity data must be JSON within 2048 UTF-8 bytes") from None
        changed = self._prune(now)
        key = (activity.channel, activity.member_id, activity.name)
        previous = self._entries.get(key)
        if previous is not None and updated <= previous.revision:
            self._schedule()
            if changed:
                self._notify()
            return False
        if previous is None and len(self._entries) >= self._max_entries:
            self._schedule()
            if changed:
                self._notify()
            raise ValueError("Activity tracker identity capacity reached")
        current = (
            None
            if expires <= now or expires == updated
            else RealtimeActivity(
                channel=activity.channel,
                member_id=activity.member_id,
                name=activity.name,
                updated_at=activity.updated_at,
                expires_at=activity.expires_at,
                data=isolated,
            )
        )
        self._entries[key] = _Entry(current, updated, expires, max(now + _WINDOW_NS, updated + _WINDOW_NS, expires))
        self._schedule()
        if changed or current is not None or previous is not None and previous.activity is not None:
            self._notify()
        return True

    def snapshot(self) -> list[RealtimeActivity]:
        """Return an isolated snapshot, pruning expired state even if a timer was delayed."""
        changed = self._prune(time.time_ns())
        self._schedule()
        result = self._current()
        if changed:
            self._notify()
        return result

    def remove_member(self, channel: str, member_id: str) -> None:
        """Apply presence departure while retaining short-lived ordering tombstones."""
        if self._disposed:
            return
        now = time.time_ns()
        changed = self._prune(now)
        for key, entry in self._entries.items():
            if key[:2] != (channel, member_id):
                continue
            changed = changed or entry.activity is not None
            entry.activity = None
            entry.revision = max(entry.revision, now)
            entry.forget_at = max(entry.forget_at, now + _WINDOW_NS)
        self._schedule()
        if changed:
            self._notify()

    def reset(self, channel: str | None = None) -> None:
        """Forget state and ordering on reconnect, for one channel or all channels."""
        if self._disposed:
            return
        changed = False
        for key in tuple(self._entries):
            if channel is None or key[0] == channel:
                changed = changed or self._entries[key].activity is not None
                del self._entries[key]
        self._schedule()
        if changed:
            self._notify()

    def dispose(self) -> None:
        """Cancel timers and clear state; subsequent apply calls fail."""
        if self._disposed:
            return
        changed = any(entry.activity is not None for entry in self._entries.values())
        self._disposed = True
        self._entries.clear()
        self._schedule()
        if changed:
            self._notify()
