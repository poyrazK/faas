from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedRealtimeNotificationTimelineEvent")


@_attrs_define
class ManagedRealtimeNotificationTimelineEvent:
    """Recorded notification delivery or lifecycle change for a principal."""

    id: int
    message_id: str
    event: str
    """Delivery status or fallback_scheduled, fallback_rescheduled, fallback_removed."""
    attempts: int
    status_code: int
    occurred_at: datetime.datetime
    not_before: datetime.datetime
    next_attempt: datetime.datetime
    device: str | Unset = UNSET
    delivery_id: str | Unset = UNSET
    reason: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        message_id = self.message_id

        event = self.event

        attempts = self.attempts

        status_code = self.status_code

        occurred_at = self.occurred_at.isoformat()

        not_before = self.not_before.isoformat()

        next_attempt = self.next_attempt.isoformat()

        device = self.device

        delivery_id = self.delivery_id

        reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "message_id": message_id,
                "event": event,
                "attempts": attempts,
                "status_code": status_code,
                "occurred_at": occurred_at,
                "not_before": not_before,
                "next_attempt": next_attempt,
            }
        )
        if device is not UNSET:
            field_dict["device"] = device
        if delivery_id is not UNSET:
            field_dict["delivery_id"] = delivery_id
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        message_id = d.pop("message_id")

        event = d.pop("event")

        attempts = d.pop("attempts")

        status_code = d.pop("status_code")

        occurred_at = datetime.datetime.fromisoformat(d.pop("occurred_at"))

        not_before = datetime.datetime.fromisoformat(d.pop("not_before"))

        next_attempt = datetime.datetime.fromisoformat(d.pop("next_attempt"))

        device = d.pop("device", UNSET)

        delivery_id = d.pop("delivery_id", UNSET)

        reason = d.pop("reason", UNSET)

        managed_realtime_notification_timeline_event = cls(
            id=id,
            message_id=message_id,
            event=event,
            attempts=attempts,
            status_code=status_code,
            occurred_at=occurred_at,
            not_before=not_before,
            next_attempt=next_attempt,
            device=device,
            delivery_id=delivery_id,
            reason=reason,
        )

        managed_realtime_notification_timeline_event.additional_properties = d
        return managed_realtime_notification_timeline_event

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
