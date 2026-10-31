from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.platform_tenant_response_status import PlatformTenantResponseStatus, check_platform_tenant_response_status

T = TypeVar("T", bound="PlatformTenantResponse")


@_attrs_define
class PlatformTenantResponse:
    """Account-level end customer; links and credentials are separate resources."""

    id: UUID
    external_ref: str
    name: str
    status: PlatformTenantResponseStatus
    created_at: datetime.datetime
    updated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        external_ref = self.external_ref

        name = self.name

        status: str = self.status

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "external_ref": external_ref,
                "name": name,
                "status": status,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        external_ref = d.pop("external_ref")

        name = d.pop("name")

        status = check_platform_tenant_response_status(d.pop("status"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        platform_tenant_response = cls(
            id=id,
            external_ref=external_ref,
            name=name,
            status=status,
            created_at=created_at,
            updated_at=updated_at,
        )

        platform_tenant_response.additional_properties = d
        return platform_tenant_response

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
