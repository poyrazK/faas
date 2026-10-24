from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..models.platform_tenant_surface_certificate_changed_webhook_payload_cert_state import (
    PlatformTenantSurfaceCertificateChangedWebhookPayloadCertState,
    check_platform_tenant_surface_certificate_changed_webhook_payload_cert_state,
)

T = TypeVar("T", bound="PlatformTenantSurfaceCertificateChangedWebhookPayload")


@_attrs_define
class PlatformTenantSurfaceCertificateChangedWebhookPayload:
    """Persisted certificate-state transition for a surface explicitly linked to this tenant. Raw provider error text,
    certificates, and private keys are never included; use the activation snapshot for current diagnostic details.

    """

    platform_tenant_id: UUID
    external_ref: str
    """The platform's durable identifier for this customer record."""
    surface_id: UUID
    surface_name: str
    app_id: UUID
    cert_state: PlatformTenantSurfaceCertificateChangedWebhookPayloadCertState
    cert_not_after: datetime.datetime | None
    """Null unless a certificate expiry is recorded."""
    changed_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        platform_tenant_id = str(self.platform_tenant_id)

        external_ref = self.external_ref

        surface_id = str(self.surface_id)

        surface_name = self.surface_name

        app_id = str(self.app_id)

        cert_state: str = self.cert_state

        cert_not_after: None | str
        if isinstance(self.cert_not_after, datetime.datetime):
            cert_not_after = self.cert_not_after.isoformat()
        else:
            cert_not_after = self.cert_not_after

        changed_at = self.changed_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "platform_tenant_id": platform_tenant_id,
                "external_ref": external_ref,
                "surface_id": surface_id,
                "surface_name": surface_name,
                "app_id": app_id,
                "cert_state": cert_state,
                "cert_not_after": cert_not_after,
                "changed_at": changed_at,
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

        cert_state = check_platform_tenant_surface_certificate_changed_webhook_payload_cert_state(d.pop("cert_state"))

        def _parse_cert_not_after(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                cert_not_after_type_0 = datetime.datetime.fromisoformat(data)

                return cert_not_after_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        cert_not_after = _parse_cert_not_after(d.pop("cert_not_after"))

        changed_at = datetime.datetime.fromisoformat(d.pop("changed_at"))

        platform_tenant_surface_certificate_changed_webhook_payload = cls(
            platform_tenant_id=platform_tenant_id,
            external_ref=external_ref,
            surface_id=surface_id,
            surface_name=surface_name,
            app_id=app_id,
            cert_state=cert_state,
            cert_not_after=cert_not_after,
            changed_at=changed_at,
        )

        return platform_tenant_surface_certificate_changed_webhook_payload
