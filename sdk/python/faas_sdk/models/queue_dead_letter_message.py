from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="QueueDeadLetterMessage")


@_attrs_define
class QueueDeadLetterMessage:
    """One row that exhausted the plan's retry budget (state='dead_letter')."""

    id: str
    created_at: datetime.datetime
    failed_at: datetime.datetime
    """When the drain transitioned the row to dead_letter."""
    attempts: int
    last_error: str
    payload: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        created_at = self.created_at.isoformat()

        failed_at = self.failed_at.isoformat()

        attempts = self.attempts

        last_error = self.last_error

        payload = self.payload

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "created_at": created_at,
                "failed_at": failed_at,
                "attempts": attempts,
                "last_error": last_error,
                "payload": payload,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        failed_at = datetime.datetime.fromisoformat(d.pop("failed_at"))

        attempts = d.pop("attempts")

        last_error = d.pop("last_error")

        payload = d.pop("payload")

        queue_dead_letter_message = cls(
            id=id,
            created_at=created_at,
            failed_at=failed_at,
            attempts=attempts,
            last_error=last_error,
            payload=payload,
        )

        queue_dead_letter_message.additional_properties = d
        return queue_dead_letter_message

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
