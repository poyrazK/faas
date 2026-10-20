from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.trigger_record_state import TriggerRecordState, check_trigger_record_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="TriggerRecord")


@_attrs_define
class TriggerRecord:
    """Audit row for one record passing through a trigger.
    Surfaced via GET /v1/triggers/{id}/records so customers can
    answer "did my last N wake-ups succeed?".

    """

    id: str
    trigger_id: str
    item_identifier: str
    """Broker-side identifier (Kafka offset, NATS seq, SQS receipt handle)."""
    payload: str
    """Raw JSON body, decoded lazily by the dashboard."""
    headers: str
    """Raw JSON of broker headers."""
    metadata: str
    """Raw JSON of broker metadata (delivery count, etc.)."""
    state: TriggerRecordState
    """Lifecycle of one trigger record."""
    attempts: int
    next_fire_at: datetime.datetime
    received_at: datetime.datetime
    last_error: None | str | Unset = UNSET
    last_dispatched_at: datetime.datetime | None | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        trigger_id = self.trigger_id

        item_identifier = self.item_identifier

        payload = self.payload

        headers = self.headers

        metadata = self.metadata

        state: str = self.state

        attempts = self.attempts

        next_fire_at = self.next_fire_at.isoformat()

        received_at = self.received_at.isoformat()

        last_error: None | str | Unset
        if isinstance(self.last_error, Unset):
            last_error = UNSET
        else:
            last_error = self.last_error

        last_dispatched_at: None | str | Unset
        if isinstance(self.last_dispatched_at, Unset):
            last_dispatched_at = UNSET
        elif isinstance(self.last_dispatched_at, datetime.datetime):
            last_dispatched_at = self.last_dispatched_at.isoformat()
        else:
            last_dispatched_at = self.last_dispatched_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "trigger_id": trigger_id,
                "item_identifier": item_identifier,
                "payload": payload,
                "headers": headers,
                "metadata": metadata,
                "state": state,
                "attempts": attempts,
                "next_fire_at": next_fire_at,
                "received_at": received_at,
            }
        )
        if last_error is not UNSET:
            field_dict["last_error"] = last_error
        if last_dispatched_at is not UNSET:
            field_dict["last_dispatched_at"] = last_dispatched_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        trigger_id = d.pop("trigger_id")

        item_identifier = d.pop("item_identifier")

        payload = d.pop("payload")

        headers = d.pop("headers")

        metadata = d.pop("metadata")

        state = check_trigger_record_state(d.pop("state"))

        attempts = d.pop("attempts")

        next_fire_at = datetime.datetime.fromisoformat(d.pop("next_fire_at"))

        received_at = datetime.datetime.fromisoformat(d.pop("received_at"))

        def _parse_last_error(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        last_error = _parse_last_error(d.pop("last_error", UNSET))

        def _parse_last_dispatched_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                last_dispatched_at_type_0 = datetime.datetime.fromisoformat(data)

                return last_dispatched_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        last_dispatched_at = _parse_last_dispatched_at(d.pop("last_dispatched_at", UNSET))

        trigger_record = cls(
            id=id,
            trigger_id=trigger_id,
            item_identifier=item_identifier,
            payload=payload,
            headers=headers,
            metadata=metadata,
            state=state,
            attempts=attempts,
            next_fire_at=next_fire_at,
            received_at=received_at,
            last_error=last_error,
            last_dispatched_at=last_dispatched_at,
        )

        trigger_record.additional_properties = d
        return trigger_record

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
