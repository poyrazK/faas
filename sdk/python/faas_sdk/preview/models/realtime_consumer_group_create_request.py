from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.realtime_consumer_group_create_request_dead_letter_policy import (
    RealtimeConsumerGroupCreateRequestDeadLetterPolicy,
    check_realtime_consumer_group_create_request_dead_letter_policy,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.realtime_consumer_group_filter import RealtimeConsumerGroupFilter


T = TypeVar("T", bound="RealtimeConsumerGroupCreateRequest")


@_attrs_define
class RealtimeConsumerGroupCreateRequest:
    """Realtime Consumer Group Create Request."""

    name: str
    after_sequence: str
    deduplication_metadata_key: str | Unset = ""
    """Immutable opt-in exact metadata field such as event_id. Empty disables assignment-time deduplication.
    Missing or empty values process normally. Within the window, the first filter-matching assigned event ID wins
    even if its snapshot later fails or expires. Retries and explicit dead-letter replay reuse the original
    snapshot. Store hashes only, at most 4096 live IDs per group; live IDs are never evicted. New IDs wait for
    capacity while existing snapshots remain claimable."""
    deduplication_window_seconds: int | Unset = 0
    """Immutable retention from first assignment. With a metadata key, omit or use zero for the 3600-second
    default; otherwise choose 60 to 86400. Without a key, must be zero. Duplicate hits do not extend expiry. After
    expiry, another copy can be assigned again."""
    lease_seconds: int | Unset = 30
    max_attempts: int | Unset = 5
    ordering_metadata_key: str | Unset = ""
    """Immutable exact valid metadata field. Empty disables ordering. Missing/empty values share one ordered
    bucket."""
    dead_letter_policy: RealtimeConsumerGroupCreateRequestDeadLetterPolicy | Unset = "block"
    """Immutable ordered-group policy. Block holds later events until replay/repair; skip lets later events
    proceed. Explicit replay can occur after later work completed."""
    dead_letter_retention_seconds: int | Unset = 0
    """Zero keeps indefinitely; otherwise 3600 to 2592000 seconds. Ordered groups require skip policy. Cleanup
    includes a 300-second warning grace."""
    priority_metadata_key: str | Unset = ""
    """Immutable opt-in metadata field. Exact single digits 0 through 9 freeze on assignment; missing or invalid
    values use zero. Higher values claim first within the bounded 128-snapshot window. Every eighth successful claim
    uses oldest eligible sequence; entity ordering remains authoritative. Empty disables priority."""
    deadline_metadata_key: str | Unset = ""
    """Immutable opt-in metadata field containing an RFC3339 delivery deadline. Ordered groups require skip policy.
    Missing or empty values have no deadline. Malformed values become terminal invalid_delivery_deadline failures.
    Frozen on assignment; existing snapshots are unchanged. Deadlines prevent expired claims and cap leases."""
    filter_: RealtimeConsumerGroupFilter | Unset = UNSET
    """Immutable selection of retained events. Event types match metadata.event_type, not the
    created/updated/deleted lifecycle event. Nonempty event_types uses OR; every metadata predicate and the event-
    type condition must match. Empty selections match everything. Missing metadata never equals an expected empty
    value. Configuration is bounded to 8192 encoded JSON bytes."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        after_sequence = self.after_sequence

        deduplication_metadata_key = self.deduplication_metadata_key

        deduplication_window_seconds = self.deduplication_window_seconds

        lease_seconds = self.lease_seconds

        max_attempts = self.max_attempts

        ordering_metadata_key = self.ordering_metadata_key

        dead_letter_policy: str | Unset = UNSET
        if not isinstance(self.dead_letter_policy, Unset):
            dead_letter_policy = self.dead_letter_policy

        dead_letter_retention_seconds = self.dead_letter_retention_seconds

        priority_metadata_key = self.priority_metadata_key

        deadline_metadata_key = self.deadline_metadata_key

        filter_: dict[str, Any] | Unset = UNSET
        if not isinstance(self.filter_, Unset):
            filter_ = self.filter_.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "after_sequence": after_sequence,
            }
        )
        if deduplication_metadata_key is not UNSET:
            field_dict["deduplication_metadata_key"] = deduplication_metadata_key
        if deduplication_window_seconds is not UNSET:
            field_dict["deduplication_window_seconds"] = deduplication_window_seconds
        if lease_seconds is not UNSET:
            field_dict["lease_seconds"] = lease_seconds
        if max_attempts is not UNSET:
            field_dict["max_attempts"] = max_attempts
        if ordering_metadata_key is not UNSET:
            field_dict["ordering_metadata_key"] = ordering_metadata_key
        if dead_letter_policy is not UNSET:
            field_dict["dead_letter_policy"] = dead_letter_policy
        if dead_letter_retention_seconds is not UNSET:
            field_dict["dead_letter_retention_seconds"] = dead_letter_retention_seconds
        if priority_metadata_key is not UNSET:
            field_dict["priority_metadata_key"] = priority_metadata_key
        if deadline_metadata_key is not UNSET:
            field_dict["deadline_metadata_key"] = deadline_metadata_key
        if filter_ is not UNSET:
            field_dict["filter"] = filter_

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.realtime_consumer_group_filter import RealtimeConsumerGroupFilter

        d = dict(src_dict)
        name = d.pop("name")

        after_sequence = d.pop("after_sequence")

        deduplication_metadata_key = d.pop("deduplication_metadata_key", UNSET)

        deduplication_window_seconds = d.pop("deduplication_window_seconds", UNSET)

        lease_seconds = d.pop("lease_seconds", UNSET)

        max_attempts = d.pop("max_attempts", UNSET)

        ordering_metadata_key = d.pop("ordering_metadata_key", UNSET)

        _dead_letter_policy = d.pop("dead_letter_policy", UNSET)
        dead_letter_policy: RealtimeConsumerGroupCreateRequestDeadLetterPolicy | Unset
        if isinstance(_dead_letter_policy, Unset):
            dead_letter_policy = UNSET
        else:
            dead_letter_policy = check_realtime_consumer_group_create_request_dead_letter_policy(_dead_letter_policy)

        dead_letter_retention_seconds = d.pop("dead_letter_retention_seconds", UNSET)

        priority_metadata_key = d.pop("priority_metadata_key", UNSET)

        deadline_metadata_key = d.pop("deadline_metadata_key", UNSET)

        _filter_ = d.pop("filter", UNSET)
        filter_: RealtimeConsumerGroupFilter | Unset
        if isinstance(_filter_, Unset):
            filter_ = UNSET
        else:
            filter_ = RealtimeConsumerGroupFilter.from_dict(_filter_)

        realtime_consumer_group_create_request = cls(
            name=name,
            after_sequence=after_sequence,
            deduplication_metadata_key=deduplication_metadata_key,
            deduplication_window_seconds=deduplication_window_seconds,
            lease_seconds=lease_seconds,
            max_attempts=max_attempts,
            ordering_metadata_key=ordering_metadata_key,
            dead_letter_policy=dead_letter_policy,
            dead_letter_retention_seconds=dead_letter_retention_seconds,
            priority_metadata_key=priority_metadata_key,
            deadline_metadata_key=deadline_metadata_key,
            filter_=filter_,
        )

        realtime_consumer_group_create_request.additional_properties = d
        return realtime_consumer_group_create_request

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
