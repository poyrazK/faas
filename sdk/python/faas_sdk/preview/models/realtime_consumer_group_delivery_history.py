from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.realtime_consumer_group_delivery_history_retention_seconds import (
    RealtimeConsumerGroupDeliveryHistoryRetentionSeconds,
    check_realtime_consumer_group_delivery_history_retention_seconds,
)

if TYPE_CHECKING:
    from ..models.realtime_consumer_group_delivery_history_event import RealtimeConsumerGroupDeliveryHistoryEvent


T = TypeVar("T", bound="RealtimeConsumerGroupDeliveryHistory")


@_attrs_define
class RealtimeConsumerGroupDeliveryHistory:
    """Realtime Consumer Group Delivery History."""

    events: list[RealtimeConsumerGroupDeliveryHistoryEvent]
    has_more: bool
    next_after_id: str
    group_pruned_events: str
    retention_seconds: RealtimeConsumerGroupDeliveryHistoryRetentionSeconds
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        events = []
        for events_item_data in self.events:
            events_item = events_item_data.to_dict()
            events.append(events_item)

        has_more = self.has_more

        next_after_id = self.next_after_id

        group_pruned_events = self.group_pruned_events

        retention_seconds: int = self.retention_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "events": events,
                "has_more": has_more,
                "next_after_id": next_after_id,
                "group_pruned_events": group_pruned_events,
                "retention_seconds": retention_seconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.realtime_consumer_group_delivery_history_event import RealtimeConsumerGroupDeliveryHistoryEvent

        d = dict(src_dict)
        events = []
        _events = d.pop("events")
        for events_item_data in _events:
            events_item = RealtimeConsumerGroupDeliveryHistoryEvent.from_dict(events_item_data)

            events.append(events_item)

        has_more = d.pop("has_more")

        next_after_id = d.pop("next_after_id")

        group_pruned_events = d.pop("group_pruned_events")

        retention_seconds = check_realtime_consumer_group_delivery_history_retention_seconds(d.pop("retention_seconds"))

        realtime_consumer_group_delivery_history = cls(
            events=events,
            has_more=has_more,
            next_after_id=next_after_id,
            group_pruned_events=group_pruned_events,
            retention_seconds=retention_seconds,
        )

        realtime_consumer_group_delivery_history.additional_properties = d
        return realtime_consumer_group_delivery_history

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
