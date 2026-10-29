from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.set_platform_tenant_credential_policy_request_allowed_scopes_item import (
    SetPlatformTenantCredentialPolicyRequestAllowedScopesItem,
    check_set_platform_tenant_credential_policy_request_allowed_scopes_item,
)

T = TypeVar("T", bound="SetPlatformTenantCredentialPolicyRequest")


@_attrs_define
class SetPlatformTenantCredentialPolicyRequest:
    """Explicit scopes and per-consumer key ceiling for tenant-bound credential self-service; empty scopes and zero disable
    it.

    """

    allowed_scopes: list[SetPlatformTenantCredentialPolicyRequestAllowedScopesItem]
    max_keys_per_consumer: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        allowed_scopes = []
        for allowed_scopes_item_data in self.allowed_scopes:
            allowed_scopes_item: str = allowed_scopes_item_data
            allowed_scopes.append(allowed_scopes_item)

        max_keys_per_consumer = self.max_keys_per_consumer

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "allowed_scopes": allowed_scopes,
                "max_keys_per_consumer": max_keys_per_consumer,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        allowed_scopes = []
        _allowed_scopes = d.pop("allowed_scopes")
        for allowed_scopes_item_data in _allowed_scopes:
            allowed_scopes_item = check_set_platform_tenant_credential_policy_request_allowed_scopes_item(
                allowed_scopes_item_data
            )

            allowed_scopes.append(allowed_scopes_item)

        max_keys_per_consumer = d.pop("max_keys_per_consumer")

        set_platform_tenant_credential_policy_request = cls(
            allowed_scopes=allowed_scopes,
            max_keys_per_consumer=max_keys_per_consumer,
        )

        set_platform_tenant_credential_policy_request.additional_properties = d
        return set_platform_tenant_credential_policy_request

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
