from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="EventReceiptCancellationResponse")


@_attrs_define
class EventReceiptCancellationResponse:
    """Durable work-policy cancel_pending operation receipt, which creates no handler invocation."""

    receipt_id: UUID
    cancelled_count: int
    created_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        receipt_id = str(self.receipt_id)

        cancelled_count = self.cancelled_count

        created_at = self.created_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "receipt_id": receipt_id,
                "cancelled_count": cancelled_count,
                "created_at": created_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        receipt_id = UUID(d.pop("receipt_id"))

        cancelled_count = d.pop("cancelled_count")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        event_receipt_cancellation_response = cls(
            receipt_id=receipt_id,
            cancelled_count=cancelled_count,
            created_at=created_at,
        )

        return event_receipt_cancellation_response
