"""Presence snapshots and expiring activity, supplied by an application's authenticated transport."""

from __future__ import annotations

import asyncio
import json
import re
import time
from collections.abc import Callable
from dataclasses import dataclass
from typing import Any, Literal

from .realtime_activity import RealtimeActivity, RealtimeActivityTracker, _copy, _timestamp


@dataclass(frozen=True, kw_only=True)
class RealtimePresenceMember:
    member_id: str
    state: dict[str, Any]
    connection_count: int


@dataclass(frozen=True, kw_only=True)
class RealtimePresenceEvent:
    channel: str
    event: Literal["snapshot", "joined", "updated", "left"]
    members: tuple[RealtimePresenceMember, ...] = ()
    member: RealtimePresenceMember | None = None
    member_id: str | None = None
    complete: bool = False


@dataclass(frozen=True, kw_only=True)
class RealtimeActivityParticipant:
    member_id: str
    state: dict[str, Any]
    connection_count: int
    activities: tuple[RealtimeActivity, ...]


@dataclass(frozen=True, kw_only=True)
class RealtimePresenceSummary:
    channel: str
    members: tuple[RealtimeActivityParticipant, ...]
    ready: bool = False

    @property
    def viewer_count(self) -> int:
        return len(self.members)

    @property
    def connection_count(self) -> int:
        return sum(member.connection_count for member in self.members)


def _channel(value: str) -> None:
    if not isinstance(value, str) or not value or len(value.encode()) > 256:
        raise ValueError("Invalid presence channel")


def _member(value: RealtimePresenceMember) -> RealtimePresenceMember:
    if (
        not isinstance(value, RealtimePresenceMember)
        or not isinstance(value.member_id, str)
        or not 1 <= len(value.member_id) <= 128
        or type(value.connection_count) is not int
        or not 1 <= value.connection_count <= 512
        or not isinstance(value.state, dict)
    ):
        raise ValueError("Invalid presence member")
    try:
        encoded = json.dumps(value.state, ensure_ascii=False, allow_nan=False, separators=(",", ":"))
        if len(encoded.encode()) > 512:
            raise ValueError()
        state = json.loads(encoded)
    except (ValueError, TypeError, OverflowError, UnicodeError, RecursionError):
        raise ValueError("Presence state must be a JSON object within 512 UTF-8 bytes") from None
    return RealtimePresenceMember(member_id=value.member_id, state=state, connection_count=value.connection_count)


class RealtimePresenceDirectory:
    """Use on one asyncio loop. Owns activity expiry; dispose on client shutdown.

    The callback receives a combined, isolated presence/activity snapshot. State is
    presentation data; member IDs must come from verified server frames.
    """

    def __init__(
        self,
        *,
        on_change: Callable[[RealtimePresenceSummary], None],
        max_members: int = 1024,
        max_activities: int = 1024,
    ):
        if type(max_members) is not int or not 1 <= max_members <= 8192:
            raise ValueError("max_members must be an integer between 1 and 8192")
        if not callable(on_change):
            raise ValueError("on_change must be callable")
        self._on_change = on_change
        self._max_members = max_members
        self._channels: dict[str, dict[str, RealtimePresenceMember]] = {}
        self._pending: dict[str, dict[str, RealtimePresenceMember]] = {}
        self._ready_channels: set[str] = set()
        self._updating = False
        self._disposed = False
        self._activity_channels: set[str] = set()
        self._tracker = RealtimeActivityTracker(on_change=self._activity_changed, max_entries=max_activities)

    def _activity_changed(self, activities: list[RealtimeActivity]) -> None:
        channels = {activity.channel for activity in activities}
        changed_channels = channels | self._activity_channels
        self._activity_channels = channels
        if not self._updating:
            for channel in sorted(changed_channels & self._channels.keys()):
                self._notify(channel)

    def _notify(self, channel: str) -> None:
        try:
            result = self._on_change(self.snapshot(channel))
            if asyncio.iscoroutine(result):
                result.close()
        except (Exception, asyncio.CancelledError):
            pass

    def snapshot(self, channel: str) -> RealtimePresenceSummary:
        _channel(channel)
        # Pruning may notify the tracker; avoid recursively generating summaries.
        previous = self._updating
        self._updating = True
        try:
            activities = self._tracker.snapshot()
        finally:
            self._updating = previous
        members = tuple(
            RealtimeActivityParticipant(
                member_id=member.member_id,
                state=json.loads(json.dumps(member.state)),
                connection_count=member.connection_count,
                activities=tuple(
                    sorted(
                        (
                            activity
                            for activity in activities
                            if activity.channel == channel and activity.member_id == member.member_id
                        ),
                        key=lambda activity: activity.name,
                    )
                ),
            )
            for member in sorted(self._channels.get(channel, {}).values(), key=lambda member: member.member_id)
        )
        return RealtimePresenceSummary(
            channel=channel,
            members=members,
            ready=channel in self._ready_channels and channel not in self._pending,
        )

    def apply(self, event: RealtimePresenceEvent) -> None:
        if self._disposed:
            raise ValueError("Presence directory is disposed")
        if not isinstance(event, RealtimePresenceEvent) or event.event not in ("snapshot", "joined", "updated", "left"):
            raise ValueError("Invalid presence event")
        _channel(event.channel)
        if type(event.complete) is not bool:
            raise ValueError("Snapshot complete must be boolean")
        if event.event == "snapshot":
            if not isinstance(event.members, (tuple, list)) or len(event.members) > self._max_members:
                raise ValueError("Invalid presence snapshot")
            incoming = [_member(member) for member in event.members]
            if len({member.member_id for member in incoming}) != len(incoming):
                raise ValueError("Duplicate presence snapshot member")
            next_members = dict(self._pending.get(event.channel, {}))
            next_members.update({member.member_id: member for member in incoming})
        else:
            next_members = dict(self._channels.get(event.channel, {}))
            if event.event == "left":
                if not isinstance(event.member_id, str) or not 1 <= len(event.member_id) <= 128:
                    raise ValueError("Presence departure requires member_id")
                next_members.pop(event.member_id, None)
            else:
                member = _member(event.member)
                next_members[member.member_id] = member
        channels, pending = dict(self._channels), dict(self._pending)
        incomplete = event.event == "snapshot" and not event.complete
        if incomplete:
            pending[event.channel] = next_members
        else:
            channels[event.channel] = next_members
            if event.event == "snapshot":
                pending.pop(event.channel, None)
            elif event.channel in pending:
                staged = dict(pending[event.channel])
                if event.event == "left":
                    staged.pop(event.member_id, None)
                else:
                    staged[member.member_id] = member
                pending[event.channel] = staged
        if (
            sum(map(len, channels.values())) + sum(map(len, pending.values())) > self._max_members
            or len(channels.keys() | pending.keys()) > self._max_members
        ):
            raise ValueError("Presence directory capacity reached")
        departed = set(self._channels.get(event.channel, {})) - set(next_members) if not incomplete else set()
        if event.event == "left":
            departed.add(event.member_id)
        self._channels, self._pending = channels, pending
        if event.event == "snapshot" and event.complete:
            self._ready_channels.add(event.channel)
        self._updating = True
        try:
            for member_id in departed:
                self._tracker.remove_member(event.channel, member_id)
        finally:
            self._updating = False
        self._notify(event.channel)

    def apply_activity(self, activity: RealtimeActivity) -> bool:
        if self._disposed:
            raise ValueError("Presence directory is disposed")
        return self._tracker.apply(activity)

    def reset(self, channel: str | None = None) -> None:
        if self._disposed:
            return
        if channel is not None:
            _channel(channel)
        changed = [name for name in self._channels.keys() | self._pending.keys() if channel is None or name == channel]
        for name in tuple(self._channels.keys() | self._pending.keys()):
            if channel is None or name == channel:
                self._channels.pop(name, None)
                self._pending.pop(name, None)
                self._ready_channels.discard(name)
        self._updating = True
        try:
            self._tracker.reset(channel)
        finally:
            self._updating = False
        for name in changed:
            self._notify(name)

    def dispose(self) -> None:
        if self._disposed:
            return
        self.reset()
        self._disposed = True
        self._tracker.dispose()


@dataclass(frozen=True, kw_only=True)
class RealtimeLabeledActivityParticipant:
    member_id: str
    label: str
    state: dict[str, Any]
    connection_count: int
    activities: tuple[RealtimeActivity, ...]


@dataclass(frozen=True, kw_only=True)
class RealtimeActivitySummary:
    channel: str
    viewers: tuple[RealtimeLabeledActivityParticipant, ...]
    active: tuple[RealtimeLabeledActivityParticipant, ...]

    @property
    def viewer_count(self) -> int:
        return len(self.viewers)

    @property
    def connection_count(self) -> int:
        return sum(member.connection_count for member in self.viewers)


def summarize_realtime_activity(
    summary: RealtimePresenceSummary,
    *,
    names: tuple[str, ...] | None = None,
    exclude_member_id: str | None = None,
    label: Callable[[RealtimePresenceMember], str] | None = None,
) -> RealtimeActivitySummary:
    """Project a presence snapshot into isolated, sorted viewers and active members.

    Labels are presentation data, never verified identity. Expired activities are
    removed even when the supplied presence summary was captured earlier.
    """
    if not isinstance(summary, RealtimePresenceSummary):
        raise ValueError("Expected RealtimePresenceSummary")
    _channel(summary.channel)
    if not isinstance(summary.members, (tuple, list)) or len(summary.members) > 8192:
        raise ValueError("Invalid presence summary members")
    if names is not None and (
        not isinstance(names, (tuple, list))
        or len(names) > 64
        or any(not isinstance(name, str) or re.fullmatch(r"[A-Za-z0-9_.:-]{1,64}", name) is None for name in names)
    ):
        raise ValueError("names must contain at most 64 valid activity names")
    if exclude_member_id is not None and (
        not isinstance(exclude_member_id, str) or not 1 <= len(exclude_member_id) <= 128
    ):
        raise ValueError("Invalid excluded member ID")
    if label is not None and not callable(label):
        raise ValueError("label must be callable")
    selected = None if names is None else set(names)
    now = time.time_ns()
    seen: set[str] = set()
    viewers = []
    total_activities = 0
    for participant in summary.members:
        if not isinstance(participant, RealtimeActivityParticipant):
            raise ValueError("Invalid activity participant")
        member = _member(
            RealtimePresenceMember(
                member_id=participant.member_id,
                state=participant.state,
                connection_count=participant.connection_count,
            )
        )
        if member.member_id in seen:
            raise ValueError("Duplicate presence member")
        seen.add(member.member_id)
        if not isinstance(participant.activities, (tuple, list)):
            raise ValueError("Invalid participant activities")
        total_activities += len(participant.activities)
        if total_activities > 8192:
            raise ValueError("Activity summary capacity reached")
        activities = []
        seen_names: set[str] = set()
        for activity in participant.activities:
            if (
                not isinstance(activity, RealtimeActivity)
                or activity.channel != summary.channel
                or activity.member_id != member.member_id
                or not isinstance(activity.name, str)
                or re.fullmatch(r"[A-Za-z0-9_.:-]{1,64}", activity.name) is None
                or activity.name in seen_names
            ):
                raise ValueError("Invalid or duplicate participant activity")
            seen_names.add(activity.name)
            expires = _timestamp(activity.expires_at)
            if expires <= now or selected is not None and activity.name not in selected:
                continue
            activities.append(_copy(activity))
        if member.member_id == exclude_member_id:
            continue
        # The label callback receives its own copy so it cannot mutate returned state.
        text = member.member_id if label is None else label(_member(member))
        if not isinstance(text, str) or len(text) > 512:
            raise ValueError("Activity labels must be strings within 512 characters")
        viewers.append(
            RealtimeLabeledActivityParticipant(
                member_id=member.member_id,
                label=text,
                state=member.state,
                connection_count=member.connection_count,
                activities=tuple(sorted(activities, key=lambda activity: activity.name)),
            )
        )
    viewers.sort(key=lambda member: member.member_id)
    return RealtimeActivitySummary(
        channel=summary.channel,
        viewers=tuple(viewers),
        active=tuple(member for member in viewers if member.activities),
    )


def format_realtime_typing_summary(summary: RealtimeActivitySummary, *, max_labels: int = 1) -> str:
    """English convenience text; use active members directly for localized UI."""
    if not isinstance(summary, RealtimeActivitySummary):
        raise ValueError("Expected RealtimeActivitySummary")
    if type(max_labels) is not int or not 1 <= max_labels <= 10:
        raise ValueError("max_labels must be an integer between 1 and 10")
    now = time.time_ns()
    typing = [
        member
        for member in summary.active
        if any(activity.name == "typing" and _timestamp(activity.expires_at) > now for activity in member.activities)
    ]
    if not typing:
        return ""
    shown = [member.label for member in typing[:max_labels]]
    remaining = len(typing) - len(shown)
    if remaining:
        text = f"{', '.join(shown)} and {remaining} other{'s' if remaining != 1 else ''}"
    elif len(shown) > 1:
        text = f"{', '.join(shown[:-1])} and {shown[-1]}"
    else:
        text = shown[0]
    return f"{text} {'is' if len(typing) == 1 else 'are'} typing."
