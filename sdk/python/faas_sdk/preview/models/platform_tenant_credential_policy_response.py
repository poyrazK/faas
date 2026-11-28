from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.platform_tenant_credential_policy_response_allowed_scopes_item import (
    PlatformTenantCredentialPolicyResponseAllowedScopesItem,
    check_platform_tenant_credential_policy_response_allowed_scopes_item,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="PlatformTenantCredentialPolicyResponse")


@_attrs_define
class PlatformTenantCredentialPolicyResponse:
    """Owner-configured scope allowlist and active-key ceiling for delegated credential management."""

    tenant_id: UUID
    enabled: bool
    allowed_scopes: list[PlatformTenantCredentialPolicyResponseAllowedScopesItem]
    max_keys_per_consumer: int
    updated_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        tenant_id = str(self.tenant_id)

        enabled = self.enabled

        allowed_scopes = []
        for allowed_scopes_item_data in self.allowed_scopes:
            allowed_scopes_item: str = allowed_scopes_item_data
            allowed_scopes.append(allowed_scopes_item)

        max_keys_per_consumer = self.max_keys_per_consumer

        updated_at: str | Unset = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "tenant_id": tenant_id,
                "enabled": enabled,
                "allowed_scopes": allowed_scopes,
                "max_keys_per_consumer": max_keys_per_consumer,
            }
        )
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        tenant_id = UUID(d.pop("tenant_id"))

        enabled = d.pop("enabled")

        allowed_scopes = []
        _allowed_scopes = d.pop("allowed_scopes")
        for allowed_scopes_item_data in _allowed_scopes:
            allowed_scopes_item = check_platform_tenant_credential_policy_response_allowed_scopes_item(
                allowed_scopes_item_data
            )

            allowed_scopes.append(allowed_scopes_item)

        max_keys_per_consumer = d.pop("max_keys_per_consumer")

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: datetime.datetime | Unset
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = datetime.datetime.fromisoformat(_updated_at)

        platform_tenant_credential_policy_response = cls(
            tenant_id=tenant_id,
            enabled=enabled,
            allowed_scopes=allowed_scopes,
            max_keys_per_consumer=max_keys_per_consumer,
            updated_at=updated_at,
        )

        platform_tenant_credential_policy_response.additional_properties = d
        return platform_tenant_credential_policy_response

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
