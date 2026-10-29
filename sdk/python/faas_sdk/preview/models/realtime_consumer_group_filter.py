from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.realtime_consumer_group_filter_metadata import RealtimeConsumerGroupFilterMetadata


T = TypeVar("T", bound="RealtimeConsumerGroupFilter")


@_attrs_define
class RealtimeConsumerGroupFilter:
    """Immutable selection of retained events. Event types match metadata.event_type, not the created/updated/deleted
    lifecycle event. Nonempty event_types uses OR; every metadata predicate and the event-type condition must match.
    Empty selections match everything. Missing metadata never equals an expected empty value. Configuration is bounded
    to 8192 encoded JSON bytes.

    """

    event_types: list[str] | Unset = UNSET
    metadata: RealtimeConsumerGroupFilterMetadata | Unset = UNSET
    """Exact case-sensitive string equality; valid metadata keys use lowercase letters, digits, underscore, hyphen
    or dot and are 1 to 64 bytes. Values are at most 128 bytes and cannot contain NUL, CR or LF. Encoded metadata is
    bounded to 4096 bytes."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        event_types: list[str] | Unset = UNSET
        if not isinstance(self.event_types, Unset):
            event_types = self.event_types

        metadata: dict[str, Any] | Unset = UNSET
        if not isinstance(self.metadata, Unset):
            metadata = self.metadata.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if event_types is not UNSET:
            field_dict["event_types"] = event_types
        if metadata is not UNSET:
            field_dict["metadata"] = metadata

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.realtime_consumer_group_filter_metadata import RealtimeConsumerGroupFilterMetadata

        d = dict(src_dict)
        event_types = cast(list[str], d.pop("event_types", UNSET))

        _metadata = d.pop("metadata", UNSET)
        metadata: RealtimeConsumerGroupFilterMetadata | Unset
        if isinstance(_metadata, Unset):
            metadata = UNSET
        else:
            metadata = RealtimeConsumerGroupFilterMetadata.from_dict(_metadata)

        realtime_consumer_group_filter = cls(
            event_types=event_types,
            metadata=metadata,
        )

        realtime_consumer_group_filter.additional_properties = d
        return realtime_consumer_group_filter

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
