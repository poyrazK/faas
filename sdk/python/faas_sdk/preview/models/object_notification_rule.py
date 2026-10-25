from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.object_notification_rule_events_item import (
    ObjectNotificationRuleEventsItem,
    check_object_notification_rule_events_item,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ObjectNotificationRule")


@_attrs_define
class ObjectNotificationRule:
    """Proven mutation routing to a Gregale function or existing enabled queue binding. Prefix and suffix are decoded
    strings; overlapping filters for the same event are rejected. IDs are generated when omitted.

    """

    destination: str
    """arn:gregale:lambda:REGION:ACCOUNT:function:APP_UUID or arn:gregale:sqs:REGION:ACCOUNT:APP_UUID/QUEUE_NAME"""
    events: list[ObjectNotificationRuleEventsItem]
    id: str | Unset = UNSET
    prefix: str | Unset = UNSET
    suffix: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        destination = self.destination

        events = []
        for events_item_data in self.events:
            events_item: str = events_item_data
            events.append(events_item)

        id = self.id

        prefix = self.prefix

        suffix = self.suffix

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "destination": destination,
                "events": events,
            }
        )
        if id is not UNSET:
            field_dict["id"] = id
        if prefix is not UNSET:
            field_dict["prefix"] = prefix
        if suffix is not UNSET:
            field_dict["suffix"] = suffix

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        destination = d.pop("destination")

        events = []
        _events = d.pop("events")
        for events_item_data in _events:
            events_item = check_object_notification_rule_events_item(events_item_data)

            events.append(events_item)

        id = d.pop("id", UNSET)

        prefix = d.pop("prefix", UNSET)

        suffix = d.pop("suffix", UNSET)

        object_notification_rule = cls(
            destination=destination,
            events=events,
            id=id,
            prefix=prefix,
            suffix=suffix,
        )

        return object_notification_rule
