from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="SetPlatformTenantConsumerProvisioningPolicyRequest")


@_attrs_define
class SetPlatformTenantConsumerProvisioningPolicyRequest:
    """Owner-controlled enablement and total active-customer ceiling for a downstream tenant."""

    enabled: bool
    max_consumers: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        max_consumers = self.max_consumers

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "enabled": enabled,
                "max_consumers": max_consumers,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        enabled = d.pop("enabled")

        max_consumers = d.pop("max_consumers")

        set_platform_tenant_consumer_provisioning_policy_request = cls(
            enabled=enabled,
            max_consumers=max_consumers,
        )

        set_platform_tenant_consumer_provisioning_policy_request.additional_properties = d
        return set_platform_tenant_consumer_provisioning_policy_request

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
