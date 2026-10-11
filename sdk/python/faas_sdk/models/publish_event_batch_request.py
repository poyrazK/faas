from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.publish_event_request import PublishEventRequest


T = TypeVar("T", bound="PublishEventBatchRequest")


@_attrs_define
class PublishEventBatchRequest:
    """Bounded collection of independently accepted event envelopes."""

    events: list[PublishEventRequest]

    def to_dict(self) -> dict[str, Any]:
        events = []
        for events_item_data in self.events:
            events_item = events_item_data.to_dict()
            events.append(events_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "events": events,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.publish_event_request import PublishEventRequest

        d = dict(src_dict)
        events = []
        _events = d.pop("events")
        for events_item_data in _events:
            events_item = PublishEventRequest.from_dict(events_item_data)

            events.append(events_item)

        publish_event_batch_request = cls(
            events=events,
        )

        return publish_event_batch_request
