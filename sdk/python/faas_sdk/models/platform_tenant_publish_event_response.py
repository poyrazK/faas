from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="PlatformTenantPublishEventResponse")


@_attrs_define
class PlatformTenantPublishEventResponse:
    """Durable acceptance receipt for a tenant-scoped published event."""

    id: UUID
    """Canonical event id scoped by tenant"""
    client_event_id: str
    """Caller-chosen event identifier."""
    accepted_at: datetime.datetime
    receipt_url: str
    """Tenant-authenticated relative URL for this event receipt."""

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        client_event_id = self.client_event_id

        accepted_at = self.accepted_at.isoformat()

        receipt_url = self.receipt_url

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "client_event_id": client_event_id,
                "accepted_at": accepted_at,
                "receipt_url": receipt_url,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        client_event_id = d.pop("client_event_id")

        accepted_at = datetime.datetime.fromisoformat(d.pop("accepted_at"))

        receipt_url = d.pop("receipt_url")

        platform_tenant_publish_event_response = cls(
            id=id,
            client_event_id=client_event_id,
            accepted_at=accepted_at,
            receipt_url=receipt_url,
        )

        return platform_tenant_publish_event_response
