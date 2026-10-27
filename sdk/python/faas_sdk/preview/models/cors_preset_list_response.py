from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.cors_preset_response import CorsPresetResponse


T = TypeVar("T", bound="CorsPresetListResponse")


@_attrs_define
class CorsPresetListResponse:
    """GET /v1/cors-presets list shape. The (account-wide,
    app-scoped) order mirrors ListCorsPresetsForAccount:
    account-wide rows first (app_id IS NULL), then
    app-scoped rows, both ordered by name.

    """

    presets: list[CorsPresetResponse]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        presets = []
        for presets_item_data in self.presets:
            presets_item = presets_item_data.to_dict()
            presets.append(presets_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "presets": presets,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.cors_preset_response import CorsPresetResponse

        d = dict(src_dict)
        presets = []
        _presets = d.pop("presets")
        for presets_item_data in _presets:
            presets_item = CorsPresetResponse.from_dict(presets_item_data)

            presets.append(presets_item)

        cors_preset_list_response = cls(
            presets=presets,
        )

        cors_preset_list_response.additional_properties = d
        return cors_preset_list_response

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
