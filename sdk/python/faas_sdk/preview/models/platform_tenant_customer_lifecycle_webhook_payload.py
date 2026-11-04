from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.platform_tenant_customer_lifecycle_webhook_payload_customer_status import (
    PlatformTenantCustomerLifecycleWebhookPayloadCustomerStatus,
    check_platform_tenant_customer_lifecycle_webhook_payload_customer_status,
)

T = TypeVar("T", bound="PlatformTenantCustomerLifecycleWebhookPayload")


@_attrs_define
class PlatformTenantCustomerLifecycleWebhookPayload:
    """Active customer identity created for or linked to a platform tenant, or later revoked. The stable
    customer_external_ref joins identities across apps; consumer_id and app_id identify the app-local row.

    """

    platform_tenant_id: UUID
    external_ref: str
    """The platform owner's stable customer reference."""
    consumer_id: UUID
    app_id: UUID
    customer_external_ref: str
    customer_name: str
    customer_status: PlatformTenantCustomerLifecycleWebhookPayloadCustomerStatus
    changed_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        platform_tenant_id = str(self.platform_tenant_id)

        external_ref = self.external_ref

        consumer_id = str(self.consumer_id)

        app_id = str(self.app_id)

        customer_external_ref = self.customer_external_ref

        customer_name = self.customer_name

        customer_status: str = self.customer_status

        changed_at = self.changed_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "platform_tenant_id": platform_tenant_id,
                "external_ref": external_ref,
                "consumer_id": consumer_id,
                "app_id": app_id,
                "customer_external_ref": customer_external_ref,
                "customer_name": customer_name,
                "customer_status": customer_status,
                "changed_at": changed_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        platform_tenant_id = UUID(d.pop("platform_tenant_id"))

        external_ref = d.pop("external_ref")

        consumer_id = UUID(d.pop("consumer_id"))

        app_id = UUID(d.pop("app_id"))

        customer_external_ref = d.pop("customer_external_ref")

        customer_name = d.pop("customer_name")

        customer_status = check_platform_tenant_customer_lifecycle_webhook_payload_customer_status(
            d.pop("customer_status")
        )

        changed_at = datetime.datetime.fromisoformat(d.pop("changed_at"))

        platform_tenant_customer_lifecycle_webhook_payload = cls(
            platform_tenant_id=platform_tenant_id,
            external_ref=external_ref,
            consumer_id=consumer_id,
            app_id=app_id,
            customer_external_ref=customer_external_ref,
            customer_name=customer_name,
            customer_status=customer_status,
            changed_at=changed_at,
        )

        return platform_tenant_customer_lifecycle_webhook_payload
