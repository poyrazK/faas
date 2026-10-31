from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="QueuePeekMessage")


@_attrs_define
class QueuePeekMessage:
    """One pending row. No lease was acquired and `attempts` was not incremented."""

    id: str
    created_at: datetime.datetime
    attempts: int
    payload: str
    """Stored payload rendered as a JSON string (verbatim from the jsonb column)."""
    last_error: str | Unset = UNSET
    """Most recent failure reason, when attempts > 0."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        created_at = self.created_at.isoformat()

        attempts = self.attempts

        payload = self.payload

        last_error = self.last_error

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "created_at": created_at,
                "attempts": attempts,
                "payload": payload,
            }
        )
        if last_error is not UNSET:
            field_dict["last_error"] = last_error

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        attempts = d.pop("attempts")

        payload = d.pop("payload")

        last_error = d.pop("last_error", UNSET)

        queue_peek_message = cls(
            id=id,
            created_at=created_at,
            attempts=attempts,
            payload=payload,
            last_error=last_error,
        )

        queue_peek_message.additional_properties = d
        return queue_peek_message

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
