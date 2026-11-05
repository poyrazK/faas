from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RealtimeTicketResponse")


@_attrs_define
class RealtimeTicketResponse:
    """Realtime Ticket Response."""

    ticket: str
    """Opaque bearer credential returned once; store only in client memory."""
    ticket_id: UUID
    """Identifies the ticket for revocation; safe to log and store."""
    expires_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        ticket = self.ticket

        ticket_id = str(self.ticket_id)

        expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "ticket": ticket,
                "ticket_id": ticket_id,
                "expires_at": expires_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        ticket = d.pop("ticket")

        ticket_id = UUID(d.pop("ticket_id"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        realtime_ticket_response = cls(
            ticket=ticket,
            ticket_id=ticket_id,
            expires_at=expires_at,
        )

        realtime_ticket_response.additional_properties = d
        return realtime_ticket_response

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
