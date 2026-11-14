from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="SupportReadResult")


@_attrs_define
class SupportReadResult:
    """Remaining unread counts for the ticket and current support scope."""

    ticket_id: UUID
    unread_count: int
    unread_total: int

    def to_dict(self) -> dict[str, Any]:
        ticket_id = str(self.ticket_id)

        unread_count = self.unread_count

        unread_total = self.unread_total

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "ticket_id": ticket_id,
                "unread_count": unread_count,
                "unread_total": unread_total,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        ticket_id = UUID(d.pop("ticket_id"))

        unread_count = d.pop("unread_count")

        unread_total = d.pop("unread_total")

        support_read_result = cls(
            ticket_id=ticket_id,
            unread_count=unread_count,
            unread_total=unread_total,
        )

        return support_read_result
