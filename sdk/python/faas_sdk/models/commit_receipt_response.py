from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="CommitReceiptResponse")


@_attrs_define
class CommitReceiptResponse:
    receipt_id: UUID
    source_id: UUID
    event_id: UUID
    invocation_id: UUID
    accepted_at: datetime.datetime
    operation_url: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        receipt_id = str(self.receipt_id)

        source_id = str(self.source_id)

        event_id = str(self.event_id)

        invocation_id = str(self.invocation_id)

        accepted_at = self.accepted_at.isoformat()

        operation_url = self.operation_url

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "receipt_id": receipt_id,
                "source_id": source_id,
                "event_id": event_id,
                "invocation_id": invocation_id,
                "accepted_at": accepted_at,
                "operation_url": operation_url,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        receipt_id = UUID(d.pop("receipt_id"))

        source_id = UUID(d.pop("source_id"))

        event_id = UUID(d.pop("event_id"))

        invocation_id = UUID(d.pop("invocation_id"))

        accepted_at = datetime.datetime.fromisoformat(d.pop("accepted_at"))

        operation_url = d.pop("operation_url")

        commit_receipt_response = cls(
            receipt_id=receipt_id,
            source_id=source_id,
            event_id=event_id,
            invocation_id=invocation_id,
            accepted_at=accepted_at,
            operation_url=operation_url,
        )

        commit_receipt_response.additional_properties = d
        return commit_receipt_response

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
