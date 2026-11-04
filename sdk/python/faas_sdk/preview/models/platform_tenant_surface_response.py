from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="PlatformTenantSurfaceResponse")


@_attrs_define
class PlatformTenantSurfaceResponse:
    """A linked tenant surface summary; managed_by_platform_tenant is true only when a platform-tenant apply created it.
    Use the app endpoint for hostnames.

    """

    id: UUID
    app_id: UUID
    name: str
    status: str
    managed_by_platform_tenant: bool | Unset = UNSET
    """True when this surface originated in the account owner's tenant bundle apply; absent otherwise."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app_id = str(self.app_id)

        name = self.name

        status = self.status

        managed_by_platform_tenant = self.managed_by_platform_tenant

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "name": name,
                "status": status,
            }
        )
        if managed_by_platform_tenant is not UNSET:
            field_dict["managed_by_platform_tenant"] = managed_by_platform_tenant

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        name = d.pop("name")

        status = d.pop("status")

        managed_by_platform_tenant = d.pop("managed_by_platform_tenant", UNSET)

        platform_tenant_surface_response = cls(
            id=id,
            app_id=app_id,
            name=name,
            status=status,
            managed_by_platform_tenant=managed_by_platform_tenant,
        )

        platform_tenant_surface_response.additional_properties = d
        return platform_tenant_surface_response

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
