from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="PlatformTenantConsumerProvisioningPolicyResponse")


@_attrs_define
class PlatformTenantConsumerProvisioningPolicyResponse:
    """Owner-controlled self-service customer creation policy; an absent policy is disabled."""

    tenant_id: UUID
    enabled: bool
    max_consumers: int
    updated_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        tenant_id = str(self.tenant_id)

        enabled = self.enabled

        max_consumers = self.max_consumers

        updated_at: str | Unset = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "tenant_id": tenant_id,
                "enabled": enabled,
                "max_consumers": max_consumers,
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

        max_consumers = d.pop("max_consumers")

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: datetime.datetime | Unset
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = datetime.datetime.fromisoformat(_updated_at)

        platform_tenant_consumer_provisioning_policy_response = cls(
            tenant_id=tenant_id,
            enabled=enabled,
            max_consumers=max_consumers,
            updated_at=updated_at,
        )

        platform_tenant_consumer_provisioning_policy_response.additional_properties = d
        return platform_tenant_consumer_provisioning_policy_response

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
