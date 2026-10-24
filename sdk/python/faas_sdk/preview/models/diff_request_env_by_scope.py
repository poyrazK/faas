from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.diff_env_row import DiffEnvRow


T = TypeVar("T", bound="DiffRequestEnvByScope")


@_attrs_define
class DiffRequestEnvByScope:
    """Per-scope env write. Full-replacement semantics per
    scope (ADR-090 D3). Keys are scope names ("default",
    "staging", ...); values are DiffEnvRow arrays.

    """

    additional_properties: dict[str, list[DiffEnvRow]] = _attrs_field(init=False, factory=dict)

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
        from ..models.diff_env_row import DiffEnvRow

        d = dict(src_dict)
        diff_request_env_by_scope = cls()

        additional_properties = {}
        for prop_name, prop_dict in d.items():
            additional_property = []
            _additional_property = prop_dict
            for additional_property_item_data in _additional_property:
                additional_property_item = DiffEnvRow.from_dict(additional_property_item_data)

                additional_property.append(additional_property_item)

            additional_properties[prop_name] = additional_property

        diff_request_env_by_scope.additional_properties = additional_properties
        return diff_request_env_by_scope

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> list[DiffEnvRow]:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: list[DiffEnvRow]) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
