from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.tenant_surface_response import TenantSurfaceResponse


T = TypeVar("T", bound="ListTenantSurfacesResponse")


@_attrs_define
class ListTenantSurfacesResponse:
    """Surfaces on the app (soft-deleted excluded server-side)."""

    surfaces: list[TenantSurfaceResponse]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        surfaces = []
        for surfaces_item_data in self.surfaces:
            surfaces_item = surfaces_item_data.to_dict()
            surfaces.append(surfaces_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "surfaces": surfaces,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.tenant_surface_response import TenantSurfaceResponse

        d = dict(src_dict)
        surfaces = []
        _surfaces = d.pop("surfaces")
        for surfaces_item_data in _surfaces:
            surfaces_item = TenantSurfaceResponse.from_dict(surfaces_item_data)

            surfaces.append(surfaces_item)

        list_tenant_surfaces_response = cls(
            surfaces=surfaces,
        )

        list_tenant_surfaces_response.additional_properties = d
        return list_tenant_surfaces_response

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
