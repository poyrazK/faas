from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.create_platform_tenant_webhook_request_delivery_format import (
    CreatePlatformTenantWebhookRequestDeliveryFormat,
    check_create_platform_tenant_webhook_request_delivery_format,
)
from ..models.create_platform_tenant_webhook_request_retry_policy import (
    CreatePlatformTenantWebhookRequestRetryPolicy,
    check_create_platform_tenant_webhook_request_retry_policy,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="CreatePlatformTenantWebhookRequest")


@_attrs_define
class CreatePlatformTenantWebhookRequest:
    """Create a receiver for this tenant's finalized cross-app billing statements.

    Example:
        {'target_url': 'https://billing.example.com/gregale/events', 'webhook_secret': 'store-this-secret-before-
            submitting'}

    """

    target_url: str
    webhook_secret: str
    retry_policy: CreatePlatformTenantWebhookRequestRetryPolicy | Unset = "default"
    delivery_format: CreatePlatformTenantWebhookRequestDeliveryFormat | Unset = "json"
    enabled: bool | Unset = True
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        target_url = self.target_url

        webhook_secret = self.webhook_secret

        retry_policy: str | Unset = UNSET
        if not isinstance(self.retry_policy, Unset):
            retry_policy = self.retry_policy

        delivery_format: str | Unset = UNSET
        if not isinstance(self.delivery_format, Unset):
            delivery_format = self.delivery_format

        enabled = self.enabled

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "target_url": target_url,
                "webhook_secret": webhook_secret,
            }
        )
        if retry_policy is not UNSET:
            field_dict["retry_policy"] = retry_policy
        if delivery_format is not UNSET:
            field_dict["delivery_format"] = delivery_format
        if enabled is not UNSET:
            field_dict["enabled"] = enabled

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        target_url = d.pop("target_url")

        webhook_secret = d.pop("webhook_secret")

        _retry_policy = d.pop("retry_policy", UNSET)
        retry_policy: CreatePlatformTenantWebhookRequestRetryPolicy | Unset
        if isinstance(_retry_policy, Unset):
            retry_policy = UNSET
        else:
            retry_policy = check_create_platform_tenant_webhook_request_retry_policy(_retry_policy)

        _delivery_format = d.pop("delivery_format", UNSET)
        delivery_format: CreatePlatformTenantWebhookRequestDeliveryFormat | Unset
        if isinstance(_delivery_format, Unset):
            delivery_format = UNSET
        else:
            delivery_format = check_create_platform_tenant_webhook_request_delivery_format(_delivery_format)

        enabled = d.pop("enabled", UNSET)

        create_platform_tenant_webhook_request = cls(
            target_url=target_url,
            webhook_secret=webhook_secret,
            retry_policy=retry_policy,
            delivery_format=delivery_format,
            enabled=enabled,
        )

        create_platform_tenant_webhook_request.additional_properties = d
        return create_platform_tenant_webhook_request

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
