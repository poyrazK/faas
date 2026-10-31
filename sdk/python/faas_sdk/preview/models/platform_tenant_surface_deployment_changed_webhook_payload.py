from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.platform_tenant_surface_deployment_changed_webhook_payload_deployment_status import (
    PlatformTenantSurfaceDeploymentChangedWebhookPayloadDeploymentStatus,
    check_platform_tenant_surface_deployment_changed_webhook_payload_deployment_status,
)

T = TypeVar("T", bound="PlatformTenantSurfaceDeploymentChangedWebhookPayload")


@_attrs_define
class PlatformTenantSurfaceDeploymentChangedWebhookPayload:
    """Terminal deployment outcome for a surface explicitly linked to this tenant. Contains revision and status metadata
    only; app/deployment IDs, source details, logs, and raw errors are excluded. A failed latest attempt does not mean a
    previous deployment is not serving.

    """

    platform_tenant_id: UUID
    external_ref: str
    surface_id: UUID
    surface_name: str
    revision: int
    deployment_status: PlatformTenantSurfaceDeploymentChangedWebhookPayloadDeploymentStatus
    started_at: datetime.datetime
    changed_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        platform_tenant_id = str(self.platform_tenant_id)

        external_ref = self.external_ref

        surface_id = str(self.surface_id)

        surface_name = self.surface_name

        revision = self.revision

        deployment_status: str = self.deployment_status

        started_at = self.started_at.isoformat()

        changed_at = self.changed_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "platform_tenant_id": platform_tenant_id,
                "external_ref": external_ref,
                "surface_id": surface_id,
                "surface_name": surface_name,
                "revision": revision,
                "deployment_status": deployment_status,
                "started_at": started_at,
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

        revision = d.pop("revision")

        deployment_status = check_platform_tenant_surface_deployment_changed_webhook_payload_deployment_status(
            d.pop("deployment_status")
        )

        started_at = datetime.datetime.fromisoformat(d.pop("started_at"))

        changed_at = datetime.datetime.fromisoformat(d.pop("changed_at"))

        platform_tenant_surface_deployment_changed_webhook_payload = cls(
            platform_tenant_id=platform_tenant_id,
            external_ref=external_ref,
            surface_id=surface_id,
            surface_name=surface_name,
            revision=revision,
            deployment_status=deployment_status,
            started_at=started_at,
            changed_at=changed_at,
        )

        return platform_tenant_surface_deployment_changed_webhook_payload
