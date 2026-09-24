from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.apply_platform_tenant_consumer_request import ApplyPlatformTenantConsumerRequest
    from ..models.apply_platform_tenant_surface_request import ApplyPlatformTenantSurfaceRequest


T = TypeVar("T", bound="ApplyPlatformTenantRequest")


@_attrs_define
class ApplyPlatformTenantRequest:
    """Additive, retry-safe onboarding of consumers and declarative hostname surfaces; dry_run validates and previews
    without writes.

    """

    external_ref: str
    name: str
    dry_run: bool | Unset = False
    consumers: list[ApplyPlatformTenantConsumerRequest] | Unset = UNSET
    surface_ids: list[UUID] | Unset = UNSET
    surfaces: list[ApplyPlatformTenantSurfaceRequest] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        external_ref = self.external_ref

        name = self.name

        dry_run = self.dry_run

        consumers: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.consumers, Unset):
            consumers = []
            for consumers_item_data in self.consumers:
                consumers_item = consumers_item_data.to_dict()
                consumers.append(consumers_item)

        surface_ids: list[str] | Unset = UNSET
        if not isinstance(self.surface_ids, Unset):
            surface_ids = []
            for surface_ids_item_data in self.surface_ids:
                surface_ids_item = str(surface_ids_item_data)
                surface_ids.append(surface_ids_item)

        surfaces: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.surfaces, Unset):
            surfaces = []
            for surfaces_item_data in self.surfaces:
                surfaces_item = surfaces_item_data.to_dict()
                surfaces.append(surfaces_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "external_ref": external_ref,
                "name": name,
            }
        )
        if dry_run is not UNSET:
            field_dict["dry_run"] = dry_run
        if consumers is not UNSET:
            field_dict["consumers"] = consumers
        if surface_ids is not UNSET:
            field_dict["surface_ids"] = surface_ids
        if surfaces is not UNSET:
            field_dict["surfaces"] = surfaces

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.apply_platform_tenant_consumer_request import ApplyPlatformTenantConsumerRequest
        from ..models.apply_platform_tenant_surface_request import ApplyPlatformTenantSurfaceRequest

        d = dict(src_dict)
        external_ref = d.pop("external_ref")

        name = d.pop("name")

        dry_run = d.pop("dry_run", UNSET)

        _consumers = d.pop("consumers", UNSET)
        consumers: list[ApplyPlatformTenantConsumerRequest] | Unset = UNSET
        if _consumers is not UNSET:
            consumers = []
            for consumers_item_data in _consumers:
                consumers_item = ApplyPlatformTenantConsumerRequest.from_dict(consumers_item_data)

                consumers.append(consumers_item)

        _surface_ids = d.pop("surface_ids", UNSET)
        surface_ids: list[UUID] | Unset = UNSET
        if _surface_ids is not UNSET:
            surface_ids = []
            for surface_ids_item_data in _surface_ids:
                surface_ids_item = UUID(surface_ids_item_data)

                surface_ids.append(surface_ids_item)

        _surfaces = d.pop("surfaces", UNSET)
        surfaces: list[ApplyPlatformTenantSurfaceRequest] | Unset = UNSET
        if _surfaces is not UNSET:
            surfaces = []
            for surfaces_item_data in _surfaces:
                surfaces_item = ApplyPlatformTenantSurfaceRequest.from_dict(surfaces_item_data)

                surfaces.append(surfaces_item)

        apply_platform_tenant_request = cls(
            external_ref=external_ref,
            name=name,
            dry_run=dry_run,
            consumers=consumers,
            surface_ids=surface_ids,
            surfaces=surfaces,
        )

        apply_platform_tenant_request.additional_properties = d
        return apply_platform_tenant_request

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
