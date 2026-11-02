from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.data_api_change_event_batch_version import (
    DataAPIChangeEventBatchVersion,
    check_data_api_change_event_batch_version,
)

if TYPE_CHECKING:
    from ..models.data_api_change_event import DataAPIChangeEvent


T = TypeVar("T", bound="DataAPIChangeEventBatch")


@_attrs_define
class DataAPIChangeEventBatch:
    """A runtime's private publication of recorded changes, in recorded order."""

    version: DataAPIChangeEventBatchVersion
    events: list[DataAPIChangeEvent]

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        events = []
        for events_item_data in self.events:
            events_item = events_item_data.to_dict()
            events.append(events_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "version": version,
                "events": events,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_api_change_event import DataAPIChangeEvent

        d = dict(src_dict)
        version = check_data_api_change_event_batch_version(d.pop("version"))

        events = []
        _events = d.pop("events")
        for events_item_data in _events:
            events_item = DataAPIChangeEvent.from_dict(events_item_data)

            events.append(events_item)

        data_api_change_event_batch = cls(
            version=version,
            events=events,
        )

        return data_api_change_event_batch
