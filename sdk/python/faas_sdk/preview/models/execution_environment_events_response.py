from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.execution_environment_event import ExecutionEnvironmentEvent


T = TypeVar("T", bound="ExecutionEnvironmentEventsResponse")


@_attrs_define
class ExecutionEnvironmentEventsResponse:
    """An ordered page of retained dependency environment build records."""

    events: list[ExecutionEnvironmentEvent]
    next_sequence: int
    status: str
    gap: bool | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        events = []
        for events_item_data in self.events:
            events_item = events_item_data.to_dict()
            events.append(events_item)

        next_sequence = self.next_sequence

        status = self.status

        gap = self.gap

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "events": events,
                "next_sequence": next_sequence,
                "status": status,
            }
        )
        if gap is not UNSET:
            field_dict["gap"] = gap

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.execution_environment_event import ExecutionEnvironmentEvent

        d = dict(src_dict)
        events = []
        _events = d.pop("events")
        for events_item_data in _events:
            events_item = ExecutionEnvironmentEvent.from_dict(events_item_data)

            events.append(events_item)

        next_sequence = d.pop("next_sequence")

        status = d.pop("status")

        gap = d.pop("gap", UNSET)

        execution_environment_events_response = cls(
            events=events,
            next_sequence=next_sequence,
            status=status,
            gap=gap,
        )

        execution_environment_events_response.additional_properties = d
        return execution_environment_events_response

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
