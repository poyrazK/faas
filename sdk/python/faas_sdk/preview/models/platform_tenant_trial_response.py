from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="PlatformTenantTrialResponse")


@_attrs_define
class PlatformTenantTrialResponse:
    """A platform tenant's free trial: its usage in [starts_at, ends_at) is priced at zero."""

    tenant_id: UUID
    starts_at: datetime.datetime
    ends_at: datetime.datetime
    active: bool
    """False once the tenant's trial has ended."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        tenant_id = str(self.tenant_id)

        starts_at = self.starts_at.isoformat()

        ends_at = self.ends_at.isoformat()

        active = self.active

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "tenant_id": tenant_id,
                "starts_at": starts_at,
                "ends_at": ends_at,
                "active": active,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        tenant_id = UUID(d.pop("tenant_id"))

        starts_at = datetime.datetime.fromisoformat(d.pop("starts_at"))

        ends_at = datetime.datetime.fromisoformat(d.pop("ends_at"))

        active = d.pop("active")

        platform_tenant_trial_response = cls(
            tenant_id=tenant_id,
            starts_at=starts_at,
            ends_at=ends_at,
            active=active,
        )

        platform_tenant_trial_response.additional_properties = d
        return platform_tenant_trial_response

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
