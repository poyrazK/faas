from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.platform_tenant_webhook_response_delivery_format import (
    PlatformTenantWebhookResponseDeliveryFormat,
    check_platform_tenant_webhook_response_delivery_format,
)
from ..models.platform_tenant_webhook_response_event_filter_item import (
    PlatformTenantWebhookResponseEventFilterItem,
    check_platform_tenant_webhook_response_event_filter_item,
)
from ..models.platform_tenant_webhook_response_retry_policy import (
    PlatformTenantWebhookResponseRetryPolicy,
    check_platform_tenant_webhook_response_retry_policy,
)
from ..models.platform_tenant_webhook_response_scope import (
    PlatformTenantWebhookResponseScope,
    check_platform_tenant_webhook_response_scope,
)
from ..models.platform_tenant_webhook_response_webhook_secret_sealed_masked import (
    PlatformTenantWebhookResponseWebhookSecretSealedMasked,
    check_platform_tenant_webhook_response_webhook_secret_sealed_masked,
)

T = TypeVar("T", bound="PlatformTenantWebhookResponse")


@_attrs_define
class PlatformTenantWebhookResponse:
    """Tenant-scoped statement event receiver. The secret is never returned."""

    id: UUID
    scope: PlatformTenantWebhookResponseScope
    platform_tenant_id: UUID
    account_id: UUID
    target_url: str
    webhook_secret_sealed_masked: PlatformTenantWebhookResponseWebhookSecretSealedMasked
    event_filter: list[PlatformTenantWebhookResponseEventFilterItem]
    retry_policy: PlatformTenantWebhookResponseRetryPolicy
    delivery_format: PlatformTenantWebhookResponseDeliveryFormat
    enabled: bool
    created_at: datetime.datetime
    updated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        scope: str = self.scope

        platform_tenant_id = str(self.platform_tenant_id)

        account_id = str(self.account_id)

        target_url = self.target_url

        webhook_secret_sealed_masked: str = self.webhook_secret_sealed_masked

        event_filter = []
        for event_filter_item_data in self.event_filter:
            event_filter_item: str = event_filter_item_data
            event_filter.append(event_filter_item)

        retry_policy: str = self.retry_policy

        delivery_format: str = self.delivery_format

        enabled = self.enabled

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "scope": scope,
                "platform_tenant_id": platform_tenant_id,
                "account_id": account_id,
                "target_url": target_url,
                "webhook_secret_sealed_masked": webhook_secret_sealed_masked,
                "event_filter": event_filter,
                "retry_policy": retry_policy,
                "delivery_format": delivery_format,
                "enabled": enabled,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        scope = check_platform_tenant_webhook_response_scope(d.pop("scope"))

        platform_tenant_id = UUID(d.pop("platform_tenant_id"))

        account_id = UUID(d.pop("account_id"))

        target_url = d.pop("target_url")

        webhook_secret_sealed_masked = check_platform_tenant_webhook_response_webhook_secret_sealed_masked(
            d.pop("webhook_secret_sealed_masked")
        )

        event_filter = []
        _event_filter = d.pop("event_filter")
        for event_filter_item_data in _event_filter:
            event_filter_item = check_platform_tenant_webhook_response_event_filter_item(event_filter_item_data)

            event_filter.append(event_filter_item)

        retry_policy = check_platform_tenant_webhook_response_retry_policy(d.pop("retry_policy"))

        delivery_format = check_platform_tenant_webhook_response_delivery_format(d.pop("delivery_format"))

        enabled = d.pop("enabled")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        platform_tenant_webhook_response = cls(
            id=id,
            scope=scope,
            platform_tenant_id=platform_tenant_id,
            account_id=account_id,
            target_url=target_url,
            webhook_secret_sealed_masked=webhook_secret_sealed_masked,
            event_filter=event_filter,
            retry_policy=retry_policy,
            delivery_format=delivery_format,
            enabled=enabled,
            created_at=created_at,
            updated_at=updated_at,
        )

        platform_tenant_webhook_response.additional_properties = d
        return platform_tenant_webhook_response

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
