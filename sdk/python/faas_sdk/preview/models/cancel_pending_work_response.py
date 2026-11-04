from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="CancelPendingWorkResponse")


@_attrs_define
class CancelPendingWorkResponse:
    """Durable receipt for a cancel-pending operation."""

    id: UUID
    cancelled_count: int

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        cancelled_count = self.cancelled_count

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "cancelled_count": cancelled_count,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        cancelled_count = d.pop("cancelled_count")

        cancel_pending_work_response = cls(
            id=id,
            cancelled_count=cancelled_count,
        )

        return cancel_pending_work_response
