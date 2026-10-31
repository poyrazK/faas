from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.realtime_consumer_group_delivery_history_event_event import (
    RealtimeConsumerGroupDeliveryHistoryEventEvent,
    check_realtime_consumer_group_delivery_history_event_event,
)

T = TypeVar("T", bound="RealtimeConsumerGroupDeliveryHistoryEvent")


@_attrs_define
class RealtimeConsumerGroupDeliveryHistoryEvent:
    """Realtime Consumer Group Delivery History Event."""

    id: str
    sequence: str
    event: RealtimeConsumerGroupDeliveryHistoryEventEvent
    attempt: int
    fencing_token: None | str
    failure_code: str
    recorded_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        sequence = self.sequence

        event: str = self.event

        attempt = self.attempt

        fencing_token: None | str
        fencing_token = self.fencing_token

        failure_code = self.failure_code

        recorded_at = self.recorded_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "sequence": sequence,
                "event": event,
                "attempt": attempt,
                "fencing_token": fencing_token,
                "failure_code": failure_code,
                "recorded_at": recorded_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        sequence = d.pop("sequence")

        event = check_realtime_consumer_group_delivery_history_event_event(d.pop("event"))

        attempt = d.pop("attempt")

        def _parse_fencing_token(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        fencing_token = _parse_fencing_token(d.pop("fencing_token"))

        failure_code = d.pop("failure_code")

        recorded_at = datetime.datetime.fromisoformat(d.pop("recorded_at"))

        realtime_consumer_group_delivery_history_event = cls(
            id=id,
            sequence=sequence,
            event=event,
            attempt=attempt,
            fencing_token=fencing_token,
            failure_code=failure_code,
            recorded_at=recorded_at,
        )

        realtime_consumer_group_delivery_history_event.additional_properties = d
        return realtime_consumer_group_delivery_history_event

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
