from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.update_platform_tenant_webhook_request_delivery_format import (
    UpdatePlatformTenantWebhookRequestDeliveryFormat,
    check_update_platform_tenant_webhook_request_delivery_format,
)
from ..models.update_platform_tenant_webhook_request_retry_policy import (
    UpdatePlatformTenantWebhookRequestRetryPolicy,
    check_update_platform_tenant_webhook_request_retry_policy,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="UpdatePlatformTenantWebhookRequest")


@_attrs_define
class UpdatePlatformTenantWebhookRequest:
    """Update a tenant statement receiver. Rotate secrets with the dedicated action."""

    target_url: str | Unset = UNSET
    retry_policy: UpdatePlatformTenantWebhookRequestRetryPolicy | Unset = UNSET
    delivery_format: UpdatePlatformTenantWebhookRequestDeliveryFormat | Unset = UNSET
    enabled: bool | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        target_url = self.target_url

        retry_policy: str | Unset = UNSET
        if not isinstance(self.retry_policy, Unset):
            retry_policy = self.retry_policy

        delivery_format: str | Unset = UNSET
        if not isinstance(self.delivery_format, Unset):
            delivery_format = self.delivery_format

        enabled = self.enabled

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if target_url is not UNSET:
            field_dict["target_url"] = target_url
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
        target_url = d.pop("target_url", UNSET)

        _retry_policy = d.pop("retry_policy", UNSET)
        retry_policy: UpdatePlatformTenantWebhookRequestRetryPolicy | Unset
        if isinstance(_retry_policy, Unset):
            retry_policy = UNSET
        else:
            retry_policy = check_update_platform_tenant_webhook_request_retry_policy(_retry_policy)

        _delivery_format = d.pop("delivery_format", UNSET)
        delivery_format: UpdatePlatformTenantWebhookRequestDeliveryFormat | Unset
        if isinstance(_delivery_format, Unset):
            delivery_format = UNSET
        else:
            delivery_format = check_update_platform_tenant_webhook_request_delivery_format(_delivery_format)

        enabled = d.pop("enabled", UNSET)

        update_platform_tenant_webhook_request = cls(
            target_url=target_url,
            retry_policy=retry_policy,
            delivery_format=delivery_format,
            enabled=enabled,
        )

        update_platform_tenant_webhook_request.additional_properties = d
        return update_platform_tenant_webhook_request

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
