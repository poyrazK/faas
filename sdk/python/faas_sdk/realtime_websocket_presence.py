"""Presence/activity projection and publication for native managed channel sockets."""

from __future__ import annotations

import json
import re
from collections.abc import Callable
from dataclasses import dataclass, field
from typing import Any

from .realtime_activity import RealtimeActivity, _timestamp
from .realtime_connection_status import RealtimeConnectionStatusTracker
from .realtime_presence import (
    RealtimePresenceDirectory,
    RealtimePresenceEvent,
    RealtimePresenceMember,
    RealtimePresenceSummary,
)
from .realtime_recovery import RealtimeRecoveryError

_REJECTIONS = {"presence_rate_limited", "signal_rate_limited", "signal_relay_unavailable"}


def _json_copy(value: Any, limit: int, *, object_only: bool = False) -> Any:
    try:
        if object_only and not isinstance(value, dict):
            raise ValueError()
        encoded = json.dumps(value, ensure_ascii=False, allow_nan=False, separators=(",", ":"))
        if len(encoded.encode()) > limit:
            raise ValueError()
        return json.loads(encoded)
    except (ValueError, TypeError, UnicodeError, RecursionError, OverflowError):
        raise ValueError(f"Expected JSON within {limit} UTF-8 bytes") from None


@dataclass(frozen=True, kw_only=True)
class RealtimeWebSocketSignal:
    channel: str
    member_id: str
    data: Any = field(repr=False)
    name: str | None = None
    updated_at: str | None = None
    expires_at: str | None = None


class RealtimeWebSocketPresence:
    """One channel's ephemeral state; reusable across its reconnects on one loop.

    on_change receives isolated combined presence/activity snapshots. Callbacks
    are synchronous and must return quickly. Scope/member IDs are server-verified
    presentation identities, not application authorization or cursor identities.
    """

    def __init__(
        self,
        *,
        on_change: Callable[[RealtimePresenceSummary], None],
        state: dict[str, Any] | None = None,
        scope: str = "connection",
        on_signal: Callable[[RealtimeWebSocketSignal], None] | None = None,
        on_rejected: Callable[[str], None] | None = None,
        max_members: int = 1024,
        max_activities: int = 1024,
    ):
        if not callable(on_change) or any(c is not None and not callable(c) for c in (on_signal, on_rejected)):
            raise ValueError("Presence callbacks must be callable")
        if scope not in ("connection", "principal"):
            raise ValueError("Presence scope must be connection or principal")
        for bound in (max_members, max_activities):
            if type(bound) is not int or not 1 <= bound <= 8192:
                raise ValueError("Presence/activity capacities must be between 1 and 8192")
        self._state = _json_copy({} if state is None else state, 512, object_only=True)
        self._scope, self._on_change = scope, on_change
        self._on_signal, self._on_rejected = on_signal, on_rejected
        self._max_members, self._max_activities = max_members, max_activities
        self._directory: RealtimePresenceDirectory | None = None
        self._channel: str | None = None
        self._session: Any = None
        self._disposed = False

    def _bind(self, session: Any, channel: str) -> None:
        if self._disposed or self._session is not None and self._session is not session:
            raise RealtimeRecoveryError("presence_already_bound_or_disposed")
        if self._channel is not None and self._channel != channel:
            raise RealtimeRecoveryError("presence_channel_changed")
        if self._directory is None:
            self._directory = RealtimePresenceDirectory(
                on_change=self._on_change, max_members=self._max_members, max_activities=self._max_activities
            )
        self._channel, self._session = channel, session

    def _subscribe_options(self) -> dict:
        return {"state": _json_copy(self._state, 512, object_only=True), "presence_scope": self._scope}

    def snapshot(self) -> RealtimePresenceSummary | None:
        if self._directory is None or self._channel is None:
            return None
        return self._directory.snapshot(self._channel)

    def _invalidate(self, session: Any) -> None:
        if self._session is not session:
            return
        self._session = None
        if self._directory is not None:
            self._directory.reset(self._channel)

    def dispose(self) -> None:
        """Clear local data/timers; close the owning subscription separately."""
        self._disposed = True
        self._session = None
        if self._directory is not None:
            self._directory.dispose()

    def _receive(self, session: Any, frame: dict) -> None:
        if self._session is not session or self._disposed:
            return
        channel = self._channel
        kind = frame.get("type")
        if kind == "presence":
            event = frame.get("event")

            def member(value: dict) -> RealtimePresenceMember:
                if not isinstance(value, dict):
                    raise ValueError("Invalid presence member")
                return RealtimePresenceMember(
                    member_id=value.get("member_id"),
                    state=value.get("state"),
                    connection_count=value.get("connection_count", 1),
                )

            if event == "snapshot":
                members = frame.get("members", [])
                if not isinstance(members, list) or len(members) > self._max_members:
                    raise ValueError("Invalid presence snapshot")
                update = RealtimePresenceEvent(
                    channel=channel,
                    event=event,
                    members=tuple(member(value) for value in members),
                    complete=frame.get("complete", False),
                )
            elif event == "left":
                update = RealtimePresenceEvent(channel=channel, event=event, member_id=frame.get("member_id"))
            else:
                update = RealtimePresenceEvent(channel=channel, event=event, member=member(frame))
            self._directory.apply(update)
        elif kind == "signal":
            identity = frame.get("member_id")
            if not isinstance(identity, str) or not 1 <= len(identity) <= 128 or "data" not in frame:
                raise ValueError("Invalid signal identity")
            data = _json_copy(frame["data"], 2048)
            name = frame.get("name")
            if any(key in frame for key in ("name", "expires_at")):
                activity = RealtimeActivity(
                    channel=channel,
                    member_id=identity,
                    name=name,
                    updated_at=frame.get("updated_at"),
                    expires_at=frame.get("expires_at"),
                    data=data,
                )
                self._directory.apply_activity(activity)
            if frame.get("updated_at") is not None:
                _timestamp(frame["updated_at"])
            if self._on_signal is not None:
                signal = RealtimeWebSocketSignal(
                    channel=channel,
                    member_id=identity,
                    data=data,
                    name=name,
                    updated_at=frame.get("updated_at"),
                    expires_at=frame.get("expires_at"),
                )
                RealtimeConnectionStatusTracker._callback(self._on_signal, signal)
        elif kind == "error" and frame.get("code") in _REJECTIONS and self._on_rejected is not None:
            RealtimeConnectionStatusTracker._callback(self._on_rejected, frame["code"])

    def _current(self) -> Any:
        session = self._session
        if self._disposed or session is None or session._closed or not session._active or session._failure is not None:
            raise RealtimeRecoveryError("presence_not_connected")
        return session

    async def update_presence(self, state: dict[str, Any]) -> None:
        """Publish state and retain it for subsequent reconnect subscriptions."""
        copied = _json_copy(state, 512, object_only=True)
        session = self._current()
        self._state = copied
        await session._send({"type": "presence", "channel": self._channel, "state": copied})

    async def send_signal(self, data: Any) -> None:
        copied = _json_copy(data, 2048)
        session = self._current()
        await session._send({"type": "signal", "channel": self._channel, "data": copied})

    async def send_activity(self, name: str, data: Any, ttl_ms: int = 5000) -> None:
        """Publish a named signal; ttl_ms=0 clears it, no automatic refresh."""
        if not isinstance(name, str) or re.fullmatch(r"[A-Za-z0-9_.:-]{1,64}", name) is None:
            raise ValueError("Invalid activity name")
        if type(ttl_ms) is not int or not 0 <= ttl_ms <= 30000:
            raise ValueError("ttl_ms must be between 0 and 30000")
        copied = _json_copy(data, 2048)
        session = self._current()
        await session._send(
            {"type": "signal", "channel": self._channel, "name": name, "data": copied, "ttl_ms": ttl_ms}
        )

    async def clear_activity(self, name: str) -> None:
        await self.send_activity(name, None, 0)
