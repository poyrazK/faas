from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ApplyPlatformTenantSelfConsumersRequest")


@_attrs_define
class ApplyPlatformTenantSelfConsumersRequest:
    """All-or-nothing customer onboarding bundle across surfaces already linked to the token's tenant; no app or tenant
    identifier is accepted.

    """

    external_ref: str
    name: str
    surface_ids: list[UUID]
    dry_run: bool | Unset = False
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        external_ref = self.external_ref

        name = self.name

        surface_ids = []
        for surface_ids_item_data in self.surface_ids:
            surface_ids_item = str(surface_ids_item_data)
            surface_ids.append(surface_ids_item)

        dry_run = self.dry_run

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "external_ref": external_ref,
                "name": name,
                "surface_ids": surface_ids,
            }
        )
        if dry_run is not UNSET:
            field_dict["dry_run"] = dry_run

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        external_ref = d.pop("external_ref")

        name = d.pop("name")

        surface_ids = []
        _surface_ids = d.pop("surface_ids")
        for surface_ids_item_data in _surface_ids:
            surface_ids_item = UUID(surface_ids_item_data)

            surface_ids.append(surface_ids_item)

        dry_run = d.pop("dry_run", UNSET)

        apply_platform_tenant_self_consumers_request = cls(
            external_ref=external_ref,
            name=name,
            surface_ids=surface_ids,
            dry_run=dry_run,
        )

        apply_platform_tenant_self_consumers_request.additional_properties = d
        return apply_platform_tenant_self_consumers_request

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
