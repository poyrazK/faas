from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="GlobalAppResolutionResponse")


@_attrs_define
class GlobalAppResolutionResponse:
    """Explicit regional API origin and app identity. Use independent regional credentials when calling that API."""

    global_app_id: UUID
    cell_id: UUID
    region: str
    api_base: str
    regional_app_id: UUID
    regional_app_slug: str
    cell_generation: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        global_app_id = str(self.global_app_id)

        cell_id = str(self.cell_id)

        region = self.region

        api_base = self.api_base

        regional_app_id = str(self.regional_app_id)

        regional_app_slug = self.regional_app_slug

        cell_generation = self.cell_generation

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "global_app_id": global_app_id,
                "cell_id": cell_id,
                "region": region,
                "api_base": api_base,
                "regional_app_id": regional_app_id,
                "regional_app_slug": regional_app_slug,
                "cell_generation": cell_generation,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        global_app_id = UUID(d.pop("global_app_id"))

        cell_id = UUID(d.pop("cell_id"))

        region = d.pop("region")

        api_base = d.pop("api_base")

        regional_app_id = UUID(d.pop("regional_app_id"))

        regional_app_slug = d.pop("regional_app_slug")

        cell_generation = d.pop("cell_generation")

        global_app_resolution_response = cls(
            global_app_id=global_app_id,
            cell_id=cell_id,
            region=region,
            api_base=api_base,
            regional_app_id=regional_app_id,
            regional_app_slug=regional_app_slug,
            cell_generation=cell_generation,
        )

        global_app_resolution_response.additional_properties = d
        return global_app_resolution_response

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
