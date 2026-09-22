from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.inbound_webhook_receipt_response_status import (
    InboundWebhookReceiptResponseStatus,
    check_inbound_webhook_receipt_response_status,
)

T = TypeVar("T", bound="InboundWebhookReceiptResponse")


@_attrs_define
class InboundWebhookReceiptResponse:
    """Returned only after the verified event has a committed durable invocation row."""

    receipt_id: UUID
    status: InboundWebhookReceiptResponseStatus
    duplicate: bool
    """True when this endpoint already accepted the same provider event ID."""
    accepted_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        receipt_id = str(self.receipt_id)

        status: str = self.status

        duplicate = self.duplicate

        accepted_at = self.accepted_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "receipt_id": receipt_id,
                "status": status,
                "duplicate": duplicate,
                "accepted_at": accepted_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        receipt_id = UUID(d.pop("receipt_id"))

        status = check_inbound_webhook_receipt_response_status(d.pop("status"))

        duplicate = d.pop("duplicate")

        accepted_at = datetime.datetime.fromisoformat(d.pop("accepted_at"))

        inbound_webhook_receipt_response = cls(
            receipt_id=receipt_id,
            status=status,
            duplicate=duplicate,
            accepted_at=accepted_at,
        )

        inbound_webhook_receipt_response.additional_properties = d
        return inbound_webhook_receipt_response

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
