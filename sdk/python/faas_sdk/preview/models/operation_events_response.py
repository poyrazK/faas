from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.operation_event import OperationEvent


T = TypeVar("T", bound="OperationEventsResponse")


@_attrs_define
class OperationEventsResponse:
    """One bounded event page, or instruction to read a fresh status snapshot."""

    events: list[OperationEvent]
    latest_sequence: int
    resync_required: bool
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        events = []
        for events_item_data in self.events:
            events_item = events_item_data.to_dict()
            events.append(events_item)

        latest_sequence = self.latest_sequence

        resync_required = self.resync_required

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "events": events,
                "latest_sequence": latest_sequence,
                "resync_required": resync_required,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_event import OperationEvent

        d = dict(src_dict)
        events = []
        _events = d.pop("events")
        for events_item_data in _events:
            events_item = OperationEvent.from_dict(events_item_data)

            events.append(events_item)

        latest_sequence = d.pop("latest_sequence")

        resync_required = d.pop("resync_required")

        operation_events_response = cls(
            events=events,
            latest_sequence=latest_sequence,
            resync_required=resync_required,
        )

        operation_events_response.additional_properties = d
        return operation_events_response

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
