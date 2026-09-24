from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.platform_tenant_detail_response_status import (
    PlatformTenantDetailResponseStatus,
    check_platform_tenant_detail_response_status,
)

if TYPE_CHECKING:
    from ..models.api_consumer_response import APIConsumerResponse
    from ..models.platform_tenant_surface_response import PlatformTenantSurfaceResponse


T = TypeVar("T", bound="PlatformTenantDetailResponse")


@_attrs_define
class PlatformTenantDetailResponse:
    """One customer and its current app-local consumer and surface links."""

    id: UUID
    external_ref: str
    name: str
    status: PlatformTenantDetailResponseStatus
    created_at: datetime.datetime
    updated_at: datetime.datetime
    consumers: list[APIConsumerResponse]
    surfaces: list[PlatformTenantSurfaceResponse]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        external_ref = self.external_ref

        name = self.name

        status: str = self.status

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        consumers = []
        for consumers_item_data in self.consumers:
            consumers_item = consumers_item_data.to_dict()
            consumers.append(consumers_item)

        surfaces = []
        for surfaces_item_data in self.surfaces:
            surfaces_item = surfaces_item_data.to_dict()
            surfaces.append(surfaces_item)

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
                "consumers": consumers,
                "surfaces": surfaces,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_consumer_response import APIConsumerResponse
        from ..models.platform_tenant_surface_response import PlatformTenantSurfaceResponse

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        external_ref = d.pop("external_ref")

        name = d.pop("name")

        status = check_platform_tenant_detail_response_status(d.pop("status"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        consumers = []
        _consumers = d.pop("consumers")
        for consumers_item_data in _consumers:
            consumers_item = APIConsumerResponse.from_dict(consumers_item_data)

            consumers.append(consumers_item)

        surfaces = []
        _surfaces = d.pop("surfaces")
        for surfaces_item_data in _surfaces:
            surfaces_item = PlatformTenantSurfaceResponse.from_dict(surfaces_item_data)

            surfaces.append(surfaces_item)

        platform_tenant_detail_response = cls(
            id=id,
            external_ref=external_ref,
            name=name,
            status=status,
            created_at=created_at,
            updated_at=updated_at,
            consumers=consumers,
            surfaces=surfaces,
        )

        platform_tenant_detail_response.additional_properties = d
        return platform_tenant_detail_response

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
