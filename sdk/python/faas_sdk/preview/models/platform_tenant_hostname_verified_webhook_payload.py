from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="PlatformTenantHostnameVerifiedWebhookPayload")


@_attrs_define
class PlatformTenantHostnameVerifiedWebhookPayload:
    """DNS ownership verification for a hostname on a surface explicitly linked to this tenant. It does not imply
    certificate issuance or routing, and never contains the DNS challenge token.

    """

    platform_tenant_id: UUID
    external_ref: str
    """Stable customer reference selected by the platform owner."""
    surface_id: UUID
    surface_name: str
    app_id: UUID
    hostname_id: UUID
    hostname: str
    verified_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        platform_tenant_id = str(self.platform_tenant_id)

        external_ref = self.external_ref

        surface_id = str(self.surface_id)

        surface_name = self.surface_name

        app_id = str(self.app_id)

        hostname_id = str(self.hostname_id)

        hostname = self.hostname

        verified_at = self.verified_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "platform_tenant_id": platform_tenant_id,
                "external_ref": external_ref,
                "surface_id": surface_id,
                "surface_name": surface_name,
                "app_id": app_id,
                "hostname_id": hostname_id,
                "hostname": hostname,
                "verified_at": verified_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        platform_tenant_id = UUID(d.pop("platform_tenant_id"))

        external_ref = d.pop("external_ref")

        surface_id = UUID(d.pop("surface_id"))

        surface_name = d.pop("surface_name")

        app_id = UUID(d.pop("app_id"))

        hostname_id = UUID(d.pop("hostname_id"))

        hostname = d.pop("hostname")

        verified_at = datetime.datetime.fromisoformat(d.pop("verified_at"))

        platform_tenant_hostname_verified_webhook_payload = cls(
            platform_tenant_id=platform_tenant_id,
            external_ref=external_ref,
            surface_id=surface_id,
            surface_name=surface_name,
            app_id=app_id,
            hostname_id=hostname_id,
            hostname=hostname,
            verified_at=verified_at,
        )

        return platform_tenant_hostname_verified_webhook_payload
