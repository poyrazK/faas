from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.flag_event import FlagEvent


T = TypeVar("T", bound="FlagEventsRequest")


@_attrs_define
class FlagEventsRequest:
    """A batch of runtime conversion events."""

    events: list[FlagEvent]

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
        from ..models.flag_event import FlagEvent

        d = dict(src_dict)
        events = []
        _events = d.pop("events")
        for events_item_data in _events:
            events_item = FlagEvent.from_dict(events_item_data)

            events.append(events_item)

        flag_events_request = cls(
            events=events,
        )

        return flag_events_request
