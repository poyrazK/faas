from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="PublishEventResponse")


@_attrs_define
class PublishEventResponse:
    """Durable acceptance receipt for a published internal event."""

    id: str
    accepted_at: datetime.datetime
    account_id: UUID

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        accepted_at = self.accepted_at.isoformat()

        account_id = str(self.account_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "accepted_at": accepted_at,
                "account_id": account_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        accepted_at = datetime.datetime.fromisoformat(d.pop("accepted_at"))

        account_id = UUID(d.pop("account_id"))

        publish_event_response = cls(
            id=id,
            accepted_at=accepted_at,
            account_id=account_id,
        )

        return publish_event_response
