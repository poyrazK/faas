from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="PublishEventResponse")


@_attrs_define
class PublishEventResponse:
    """Durable acceptance receipt for a published internal event."""

    id: str
    accepted_at: datetime.datetime
    account_id: UUID
    receipt_url: str
    """Account-authenticated relative URL for this event receipt."""
    client_event_id: str | Unset = UNSET
    """Original caller-chosen event identifier when tenant-scoped publication is used."""

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        accepted_at = self.accepted_at.isoformat()

        account_id = str(self.account_id)

        receipt_url = self.receipt_url

        client_event_id = self.client_event_id

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "accepted_at": accepted_at,
                "account_id": account_id,
                "receipt_url": receipt_url,
            }
        )
        if client_event_id is not UNSET:
            field_dict["client_event_id"] = client_event_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        accepted_at = datetime.datetime.fromisoformat(d.pop("accepted_at"))

        account_id = UUID(d.pop("account_id"))

        receipt_url = d.pop("receipt_url")

        client_event_id = d.pop("client_event_id", UNSET)

        publish_event_response = cls(
            id=id,
            accepted_at=accepted_at,
            account_id=account_id,
            receipt_url=receipt_url,
            client_event_id=client_event_id,
        )

        return publish_event_response
