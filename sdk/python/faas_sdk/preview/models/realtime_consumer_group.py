from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.realtime_consumer_group_dead_letter_policy import (
    RealtimeConsumerGroupDeadLetterPolicy,
    check_realtime_consumer_group_dead_letter_policy,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.realtime_consumer_group_filter import RealtimeConsumerGroupFilter


T = TypeVar("T", bound="RealtimeConsumerGroup")


@_attrs_define
class RealtimeConsumerGroup:
    """Realtime Consumer Group."""

    deduplication_metadata_key: str
    """Immutable event ID metadata field; empty disables deduplication."""
    deduplication_window_seconds: int
    """Retention from first assignment; zero when disabled."""
    duplicate_messages: str
    """Cumulative source events skipped as duplicate IDs. Separate from filtering and completions; no snapshots,
    lease attempts, claim-rate credit or fencing tokens are allocated."""
    deduplication_keys: int
    """Currently unexpired remembered event ID hashes. Idle expired rows are pruned on the next keyed assignment."""
    group_id: str
    name: str
    channel: str
    assigned_sequence: str
    latest_sequence: str
    oldest_sequence: str
    unassigned: str
    completed: str
    ready: int
    leased: int
    expired_leases: int
    dead: int
    active_workers: int
    history_unavailable: bool
    capacity_blocked: bool
    lease_seconds: int
    max_attempts: int
    created_at: datetime.datetime
    paused: bool
    """New claims are paused; existing leases remain valid."""
    paused_at: datetime.datetime | None
    draining: bool
    drained: bool
    """Drain mode is active and no unexpired leases remain; not a handler completion receipt."""
    ordering_blocked: int
    """Ready snapshots held by ordering predecessors or leases; ready counts still include them."""
    last_fencing_token: str
    """Latest committed fencing number for this group UUID; starts at zero."""
    expired_dead_letters: str
    """Cumulative snapshots removed by retention; not successful completions."""
    expired_deliveries: str
    """Cumulative deadline-expired or malformed-deadline deliveries made terminal by claim/replay maintenance. Not
    successful completions."""
    filter_: RealtimeConsumerGroupFilter
    """Immutable selection of retained events. Event types match metadata.event_type, not the
    created/updated/deleted lifecycle event. Nonempty event_types uses OR; every metadata predicate and the event-
    type condition must match. Empty selections match everything. Missing metadata never equals an expected empty
    value. Configuration is bounded to 8192 encoded JSON bytes."""
    filtered_messages: str
    """Cumulative nonmatching retained events skipped during assignment; does not count completions or explicit
    retention-gap resynchronization."""
    ordering_metadata_key: str = ""
    """this realtime operation. Immutable exact valid metadata field. Empty disables ordering. Missing/empty values
    share one ordered bucket."""
    dead_letter_policy: RealtimeConsumerGroupDeadLetterPolicy = "block"
    """this realtime operation. Immutable ordered-group policy. Block holds later events until replay/repair; skip
    lets later events proceed. Explicit replay can occur after later work completed."""
    dead_letter_retention_seconds: int = 0
    """Realtime consumer group properties dead letter retention seconds. Zero keeps indefinitely; otherwise 3600 to
    2592000 seconds. Ordered groups require skip policy. Cleanup includes a 300-second warning grace."""
    deadline_metadata_key: str = ""
    """this realtime operation. Immutable opt-in metadata field containing an RFC3339 delivery deadline. Ordered
    groups require skip policy. Missing or empty values have no deadline. Malformed values become terminal
    invalid_delivery_deadline failures. Frozen on assignment; existing snapshots are unchanged. Deadlines prevent
    expired claims and cap leases."""
    trigger_id: UUID | Unset = UNSET
    """Realtime trigger consuming this group. Absent when workers consume it directly."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deduplication_metadata_key = self.deduplication_metadata_key

        deduplication_window_seconds = self.deduplication_window_seconds

        duplicate_messages = self.duplicate_messages

        deduplication_keys = self.deduplication_keys

        group_id = self.group_id

        name = self.name

        channel = self.channel

        assigned_sequence = self.assigned_sequence

        latest_sequence = self.latest_sequence

        oldest_sequence = self.oldest_sequence

        unassigned = self.unassigned

        completed = self.completed

        ready = self.ready

        leased = self.leased

        expired_leases = self.expired_leases

        dead = self.dead

        active_workers = self.active_workers

        history_unavailable = self.history_unavailable

        capacity_blocked = self.capacity_blocked

        lease_seconds = self.lease_seconds

        max_attempts = self.max_attempts

        created_at = self.created_at.isoformat()

        paused = self.paused

        paused_at: None | str
        if isinstance(self.paused_at, datetime.datetime):
            paused_at = self.paused_at.isoformat()
        else:
            paused_at = self.paused_at

        draining = self.draining

        drained = self.drained

        ordering_metadata_key = self.ordering_metadata_key

        dead_letter_policy: str = self.dead_letter_policy

        ordering_blocked = self.ordering_blocked

        last_fencing_token = self.last_fencing_token

        dead_letter_retention_seconds = self.dead_letter_retention_seconds

        expired_dead_letters = self.expired_dead_letters

        deadline_metadata_key = self.deadline_metadata_key

        expired_deliveries = self.expired_deliveries

        filter_ = self.filter_.to_dict()

        filtered_messages = self.filtered_messages

        trigger_id: str | Unset = UNSET
        if not isinstance(self.trigger_id, Unset):
            trigger_id = str(self.trigger_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deduplication_metadata_key": deduplication_metadata_key,
                "deduplication_window_seconds": deduplication_window_seconds,
                "duplicate_messages": duplicate_messages,
                "deduplication_keys": deduplication_keys,
                "group_id": group_id,
                "name": name,
                "channel": channel,
                "assigned_sequence": assigned_sequence,
                "latest_sequence": latest_sequence,
                "oldest_sequence": oldest_sequence,
                "unassigned": unassigned,
                "completed": completed,
                "ready": ready,
                "leased": leased,
                "expired_leases": expired_leases,
                "dead": dead,
                "active_workers": active_workers,
                "history_unavailable": history_unavailable,
                "capacity_blocked": capacity_blocked,
                "lease_seconds": lease_seconds,
                "max_attempts": max_attempts,
                "created_at": created_at,
                "paused": paused,
                "paused_at": paused_at,
                "draining": draining,
                "drained": drained,
                "ordering_metadata_key": ordering_metadata_key,
                "dead_letter_policy": dead_letter_policy,
                "ordering_blocked": ordering_blocked,
                "last_fencing_token": last_fencing_token,
                "dead_letter_retention_seconds": dead_letter_retention_seconds,
                "expired_dead_letters": expired_dead_letters,
                "deadline_metadata_key": deadline_metadata_key,
                "expired_deliveries": expired_deliveries,
                "filter": filter_,
                "filtered_messages": filtered_messages,
            }
        )
        if trigger_id is not UNSET:
            field_dict["trigger_id"] = trigger_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.realtime_consumer_group_filter import RealtimeConsumerGroupFilter

        d = dict(src_dict)
        deduplication_metadata_key = d.pop("deduplication_metadata_key")

        deduplication_window_seconds = d.pop("deduplication_window_seconds")

        duplicate_messages = d.pop("duplicate_messages")

        deduplication_keys = d.pop("deduplication_keys")

        group_id = d.pop("group_id")

        name = d.pop("name")

        channel = d.pop("channel")

        assigned_sequence = d.pop("assigned_sequence")

        latest_sequence = d.pop("latest_sequence")

        oldest_sequence = d.pop("oldest_sequence")

        unassigned = d.pop("unassigned")

        completed = d.pop("completed")

        ready = d.pop("ready")

        leased = d.pop("leased")

        expired_leases = d.pop("expired_leases")

        dead = d.pop("dead")

        active_workers = d.pop("active_workers")

        history_unavailable = d.pop("history_unavailable")

        capacity_blocked = d.pop("capacity_blocked")

        lease_seconds = d.pop("lease_seconds")

        max_attempts = d.pop("max_attempts")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        paused = d.pop("paused")

        def _parse_paused_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                paused_at_type_0 = datetime.datetime.fromisoformat(data)

                return paused_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        paused_at = _parse_paused_at(d.pop("paused_at"))

        draining = d.pop("draining")

        drained = d.pop("drained")

        ordering_metadata_key = d.pop("ordering_metadata_key")

        dead_letter_policy = check_realtime_consumer_group_dead_letter_policy(d.pop("dead_letter_policy"))

        ordering_blocked = d.pop("ordering_blocked")

        last_fencing_token = d.pop("last_fencing_token")

        dead_letter_retention_seconds = d.pop("dead_letter_retention_seconds")

        expired_dead_letters = d.pop("expired_dead_letters")

        deadline_metadata_key = d.pop("deadline_metadata_key")

        expired_deliveries = d.pop("expired_deliveries")

        filter_ = RealtimeConsumerGroupFilter.from_dict(d.pop("filter"))

        filtered_messages = d.pop("filtered_messages")

        _trigger_id = d.pop("trigger_id", UNSET)
        trigger_id: UUID | Unset
        if isinstance(_trigger_id, Unset):
            trigger_id = UNSET
        else:
            trigger_id = UUID(_trigger_id)

        realtime_consumer_group = cls(
            deduplication_metadata_key=deduplication_metadata_key,
            deduplication_window_seconds=deduplication_window_seconds,
            duplicate_messages=duplicate_messages,
            deduplication_keys=deduplication_keys,
            group_id=group_id,
            name=name,
            channel=channel,
            assigned_sequence=assigned_sequence,
            latest_sequence=latest_sequence,
            oldest_sequence=oldest_sequence,
            unassigned=unassigned,
            completed=completed,
            ready=ready,
            leased=leased,
            expired_leases=expired_leases,
            dead=dead,
            active_workers=active_workers,
            history_unavailable=history_unavailable,
            capacity_blocked=capacity_blocked,
            lease_seconds=lease_seconds,
            max_attempts=max_attempts,
            created_at=created_at,
            paused=paused,
            paused_at=paused_at,
            draining=draining,
            drained=drained,
            ordering_metadata_key=ordering_metadata_key,
            dead_letter_policy=dead_letter_policy,
            ordering_blocked=ordering_blocked,
            last_fencing_token=last_fencing_token,
            dead_letter_retention_seconds=dead_letter_retention_seconds,
            expired_dead_letters=expired_dead_letters,
            deadline_metadata_key=deadline_metadata_key,
            expired_deliveries=expired_deliveries,
            filter_=filter_,
            filtered_messages=filtered_messages,
            trigger_id=trigger_id,
        )

        realtime_consumer_group.additional_properties = d
        return realtime_consumer_group

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
