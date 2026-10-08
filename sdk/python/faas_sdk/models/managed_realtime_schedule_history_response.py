from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.managed_realtime_schedule_history_event import ManagedRealtimeScheduleHistoryEvent


T = TypeVar("T", bound="ManagedRealtimeScheduleHistoryResponse")


@_attrs_define
class ManagedRealtimeScheduleHistoryResponse:
    schedule_id: str
    channel: str
    oldest_version: int
    latest_version: int
    history_truncated: bool
    has_more: bool
    events: list[ManagedRealtimeScheduleHistoryEvent]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        schedule_id = self.schedule_id

        channel = self.channel

        oldest_version = self.oldest_version

        latest_version = self.latest_version

        history_truncated = self.history_truncated

        has_more = self.has_more

        events = []
        for events_item_data in self.events:
            events_item = events_item_data.to_dict()
            events.append(events_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "schedule_id": schedule_id,
                "channel": channel,
                "oldest_version": oldest_version,
                "latest_version": latest_version,
                "history_truncated": history_truncated,
                "has_more": has_more,
                "events": events,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_realtime_schedule_history_event import ManagedRealtimeScheduleHistoryEvent

        d = dict(src_dict)
        schedule_id = d.pop("schedule_id")

        channel = d.pop("channel")

        oldest_version = d.pop("oldest_version")

        latest_version = d.pop("latest_version")

        history_truncated = d.pop("history_truncated")

        has_more = d.pop("has_more")

        events = []
        _events = d.pop("events")
        for events_item_data in _events:
            events_item = ManagedRealtimeScheduleHistoryEvent.from_dict(events_item_data)

            events.append(events_item)

        managed_realtime_schedule_history_response = cls(
            schedule_id=schedule_id,
            channel=channel,
            oldest_version=oldest_version,
            latest_version=latest_version,
            history_truncated=history_truncated,
            has_more=has_more,
            events=events,
        )

        managed_realtime_schedule_history_response.additional_properties = d
        return managed_realtime_schedule_history_response

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
