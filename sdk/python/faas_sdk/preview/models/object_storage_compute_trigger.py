from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.object_storage_compute_trigger_events_item import (
    ObjectStorageComputeTriggerEventsItem,
    check_object_storage_compute_trigger_events_item,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ObjectStorageComputeTrigger")


@_attrs_define
class ObjectStorageComputeTrigger:
    """Stable ObjectCreated filter targeting only the bucket's owning app."""

    id: str
    """Stable manifest identity scoped to the bucket."""
    events: list[ObjectStorageComputeTriggerEventsItem] | Unset = UNSET
    """Defaults to s3:ObjectCreated:* when omitted or empty."""
    prefix: str | Unset = UNSET
    suffix: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        events: list[str] | Unset = UNSET
        if not isinstance(self.events, Unset):
            events = []
            for events_item_data in self.events:
                events_item: str = events_item_data
                events.append(events_item)

        prefix = self.prefix

        suffix = self.suffix

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
            }
        )
        if events is not UNSET:
            field_dict["events"] = events
        if prefix is not UNSET:
            field_dict["prefix"] = prefix
        if suffix is not UNSET:
            field_dict["suffix"] = suffix

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        _events = d.pop("events", UNSET)
        events: list[ObjectStorageComputeTriggerEventsItem] | Unset = UNSET
        if _events is not UNSET:
            events = []
            for events_item_data in _events:
                events_item = check_object_storage_compute_trigger_events_item(events_item_data)

                events.append(events_item)

        prefix = d.pop("prefix", UNSET)

        suffix = d.pop("suffix", UNSET)

        object_storage_compute_trigger = cls(
            id=id,
            events=events,
            prefix=prefix,
            suffix=suffix,
        )

        return object_storage_compute_trigger
