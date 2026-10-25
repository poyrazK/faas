from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.scoped_app_env_response import ScopedAppEnvResponse


T = TypeVar("T", bound="AppEnvListResponseEnvByScope")


@_attrs_define
class AppEnvListResponseEnvByScope:
    """Nested per-scope map (ADR-090 PR-B D3). Populated only when `?scope=__all__` is passed; keys are scope names, values
    are per-scope row lists ordered by key ASC.

    """

    additional_properties: dict[str, list[ScopedAppEnvResponse]] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}
        for prop_name, prop in self.additional_properties.items():
            field_dict[prop_name] = []
            for additional_property_item_data in prop:
                additional_property_item = additional_property_item_data.to_dict()
                field_dict[prop_name].append(additional_property_item)

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.scoped_app_env_response import ScopedAppEnvResponse

        d = dict(src_dict)
        app_env_list_response_env_by_scope = cls()

        additional_properties = {}
        for prop_name, prop_dict in d.items():
            additional_property = []
            _additional_property = prop_dict
            for additional_property_item_data in _additional_property:
                additional_property_item = ScopedAppEnvResponse.from_dict(additional_property_item_data)

                additional_property.append(additional_property_item)

            additional_properties[prop_name] = additional_property

        app_env_list_response_env_by_scope.additional_properties = additional_properties
        return app_env_list_response_env_by_scope

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> list[ScopedAppEnvResponse]:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: list[ScopedAppEnvResponse]) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
