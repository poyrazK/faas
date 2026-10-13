"""Observe requested subscriptions independently of transport connectivity."""

from __future__ import annotations

import asyncio
import math
import re
import time
from collections.abc import Callable
from dataclasses import dataclass, replace
from typing import Literal

from .realtime_connection_status import RealtimeConnectionStatusTracker


@dataclass(frozen=True, kw_only=True)
class RealtimeSubscriptionStatus:
    channel: str | None
    inbox: bool
    state: Literal["pending", "subscribed", "denied", "resync_required"]
    code: str | None = None
    acknowledgement_missing: bool = False
    pending_age_seconds: float | None = None


@dataclass(frozen=True, kw_only=True)
class RealtimeSubscriptionSummary:
    attempt_id: int
    generation: int
    subscriptions: tuple[RealtimeSubscriptionStatus, ...]
    ready: bool


class RealtimeSubscriptionWaitError(Exception):
    """Terminal readiness result without raw response/transport details."""

    def __init__(
        self,
        reason: Literal["denied", "resync_required", "obsolete", "disposed"],
        subscriptions: tuple[RealtimeSubscriptionStatus, ...] = (),
    ):
        super().__init__(f"Subscription readiness wait failed: {reason}")
        self.reason = reason
        self.subscriptions = subscriptions


def _identity(channel: str | None, inbox: bool) -> tuple[bool, str | None]:
    if (
        type(inbox) is not bool
        or inbox
        and channel is not None
        or not inbox
        and (not isinstance(channel, str) or not channel or len(channel.encode()) > 256)
    ):
        raise ValueError("Choose a valid channel or principal inbox")
    return inbox, channel


class RealtimeSubscriptionTracker:
    """One endpoint/account on one execution context; no subscribe or retry writes."""

    def __init__(
        self,
        *,
        on_change: Callable[[RealtimeSubscriptionSummary], None],
        max_subscriptions: int = 1024,
        acknowledgement_timeout_seconds: float | None = None,
        max_waiters: int = 128,
    ):
        if not callable(on_change):
            raise ValueError("on_change must be callable")
        if type(max_subscriptions) is not int or not 1 <= max_subscriptions <= 8192:
            raise ValueError("max_subscriptions must be an integer between 1 and 8192")
        value = acknowledgement_timeout_seconds
        if value is not None and (
            isinstance(value, bool)
            or not isinstance(value, (int, float))
            or not math.isfinite(value)
            or not 0.1 <= value <= 86400
        ):
            raise ValueError("acknowledgement_timeout_seconds must be between 0.1 and 86400, or None")
        self._on_change, self._capacity, self._timeout = on_change, max_subscriptions, value
        self._attempt = self._generation = 0
        self._started: float | None = None
        self._entries: dict[tuple[bool, str | None], RealtimeSubscriptionStatus] = {}
        if type(max_waiters) is not int or not 1 <= max_waiters <= 8192:
            raise ValueError("max_waiters must be an integer between 1 and 8192")
        self._max_waiters = max_waiters
        self._waiting_count = 0
        self._wait_loop: asyncio.AbstractEventLoop | None = None
        self._waiters: set[asyncio.Future[None]] = set()
        self._disposed = False

    def begin(self, attempt_id: int, *, channels: tuple[str, ...], include_inbox: bool = False) -> int | None:
        """Replace the required set atomically; capture the returned callback generation."""
        if self._disposed:
            raise ValueError("Subscription tracker is disposed")
        if type(attempt_id) is not int or attempt_id < 1:
            raise ValueError("attempt_id must be a positive integer")
        if type(include_inbox) is not bool or not isinstance(channels, (tuple, list)) or len(channels) > self._capacity:
            raise ValueError("Invalid requested subscriptions")
        keys = [_identity(channel, False) for channel in channels]
        if include_inbox:
            keys.append((True, None))
        if len(set(keys)) != len(keys):
            raise ValueError("Duplicate requested channel")
        if len(keys) > self._capacity:
            raise ValueError("Subscription capacity reached")
        if attempt_id < self._attempt:
            return None
        self._attempt = attempt_id
        self._generation += 1
        self._started = time.monotonic()
        self._entries = {
            key: RealtimeSubscriptionStatus(channel=key[1], inbox=key[0], state="pending", pending_age_seconds=0.0)
            for key in keys
        }
        self._notify()
        return self._generation

    def _update(self, generation: int, channel: str | None, inbox: bool, state: str, code: str | None = None) -> bool:
        key = _identity(channel, inbox)
        if type(generation) is not int or generation < 1:
            raise ValueError("generation must be a positive integer")
        if self._disposed or generation != self._generation:
            return False
        previous = self._entries.get(key)
        if previous is None:
            raise ValueError("Subscription was not requested")
        if previous.state in ("denied", "resync_required") or previous.state == state:
            return False
        if state in ("subscribed", "denied") and previous.state != "pending":
            return False
        self._entries[key] = RealtimeSubscriptionStatus(channel=channel, inbox=inbox, state=state, code=code)
        self._notify()
        return True

    def subscribed(self, generation: int, *, channel: str | None = None, inbox: bool = False) -> bool:
        return self._update(generation, channel, inbox, "subscribed")

    def denied(
        self, generation: int, *, channel: str | None = None, inbox: bool = False, code: str = "subscription_denied"
    ) -> bool:
        if not isinstance(code, str) or re.fullmatch(r"[a-z0-9_]{1,128}", code) is None:
            raise ValueError("Subscription error code must be safe lowercase ASCII")
        return self._update(generation, channel, inbox, "denied", code)

    def require_resync(self, generation: int, *, channel: str | None = None, inbox: bool = False) -> bool:
        return self._update(generation, channel, inbox, "resync_required")

    def _view(self) -> RealtimeSubscriptionSummary:
        return RealtimeSubscriptionSummary(
            attempt_id=self._attempt,
            generation=self._generation,
            subscriptions=tuple(sorted(self._entries.values(), key=lambda entry: (entry.inbox, entry.channel or ""))),
            ready=self._started is not None and all(entry.state == "subscribed" for entry in self._entries.values()),
        )

    def _notify(self) -> None:
        for waiter in tuple(self._waiters):
            if not waiter.done():
                waiter.set_result(None)
        RealtimeConnectionStatusTracker._callback(self._on_change, self._view())

    def snapshot(self) -> RealtimeSubscriptionSummary:
        """Evaluate missing acknowledgements through an existing UI/lifecycle tick."""
        if not self._disposed and self._started is not None:
            age = max(0.0, time.monotonic() - self._started)
            changed = False
            for key, entry in self._entries.items():
                if entry.state != "pending":
                    continue
                missing = self._timeout is not None and age >= self._timeout
                changed = changed or missing != entry.acknowledgement_missing
                self._entries[key] = replace(entry, pending_age_seconds=age, acknowledgement_missing=missing)
            if changed:
                self._notify()
        return self._view()

    async def wait_ready(
        self,
        generation: int,
        *,
        channels: tuple[str, ...] | None = None,
        include_inbox: bool | None = None,
        timeout_seconds: float | None = None,
    ) -> RealtimeSubscriptionSummary:
        """Await selected subscriptions for one generation; this sends no requests.

        channels=None selects every requested channel. include_inbox=None selects
        the inbox only if requested. Explicit selections must already be requested.
        Success returns a selected ready summary, not global readiness.
        """
        if type(generation) is not int or generation < 1:
            raise ValueError("generation must be a positive integer")
        if include_inbox is not None and type(include_inbox) is not bool:
            raise ValueError("include_inbox must be boolean or None")
        if timeout_seconds is not None and (
            isinstance(timeout_seconds, bool)
            or not isinstance(timeout_seconds, (int, float))
            or not math.isfinite(timeout_seconds)
            or not 0 <= timeout_seconds <= 86400
        ):
            raise ValueError("timeout_seconds must be between 0 and 86400, or None")
        if self._disposed:
            raise RealtimeSubscriptionWaitError("disposed")
        if generation != self._generation or self._started is None:
            raise RealtimeSubscriptionWaitError("obsolete")
        if channels is None:
            keys = {key for key in self._entries if not key[0]}
        else:
            if not isinstance(channels, (tuple, list)) or len(channels) > self._capacity:
                raise ValueError("Invalid selected subscriptions")
            keys = {_identity(channel, False) for channel in channels}
            if len(keys) != len(channels):
                raise ValueError("Duplicate selected channel")
        if include_inbox is True or include_inbox is None and (True, None) in self._entries:
            keys.add((True, None))
        if not keys <= self._entries.keys():
            raise ValueError("Selected subscription was not requested")
        loop = asyncio.get_running_loop()
        if self._wait_loop is not None and self._wait_loop is not loop:
            raise ValueError("Subscription waits must use one asyncio loop")
        if self._waiting_count >= self._max_waiters:
            raise ValueError("Subscription waiter capacity reached")
        self._wait_loop = loop
        self._waiting_count += 1
        try:
            async with asyncio.timeout(timeout_seconds):
                while True:
                    if self._disposed:
                        raise RealtimeSubscriptionWaitError("disposed")
                    if generation != self._generation or self._started is None:
                        raise RealtimeSubscriptionWaitError("obsolete")
                    selected = tuple(
                        sorted(
                            (self._entries[key] for key in keys), key=lambda entry: (entry.inbox, entry.channel or "")
                        )
                    )
                    denied = tuple(entry for entry in selected if entry.state == "denied")
                    resync = tuple(entry for entry in selected if entry.state == "resync_required")
                    if denied:
                        raise RealtimeSubscriptionWaitError("denied", denied)
                    if resync:
                        raise RealtimeSubscriptionWaitError("resync_required", resync)
                    if all(entry.state == "subscribed" for entry in selected):
                        return RealtimeSubscriptionSummary(
                            attempt_id=self._attempt,
                            generation=generation,
                            subscriptions=selected,
                            ready=True,
                        )
                    waiter = loop.create_future()
                    self._waiters.add(waiter)
                    try:
                        await waiter
                    finally:
                        self._waiters.discard(waiter)
        finally:
            self._waiting_count -= 1

    def reset(self) -> None:
        """Invalidate callbacks on reconnect; begin must reestablish the required set."""
        if self._disposed:
            return
        self._generation += 1
        self._started = None
        self._entries.clear()
        self._notify()

    def dispose(self) -> None:
        if self._disposed:
            return
        self._disposed = True
        self._generation += 1
        self._started = None
        self._entries.clear()
        self._notify()
