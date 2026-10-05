from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="SimulateAutomationRequestMockItemOutputs")


@_attrs_define
class SimulateAutomationRequestMockItemOutputs:
    """Ordered successful output prefix keyed by for_each root name; waits and controls cannot be mocked."""

    additional_properties: dict[str, list[Any]] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}
        for prop_name, prop in self.additional_properties.items():
            field_dict[prop_name] = prop

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        simulate_automation_request_mock_item_outputs = cls()

        additional_properties = {}
        for prop_name, prop_dict in d.items():
            additional_property = cast(list[Any], prop_dict)

            additional_properties[prop_name] = additional_property

        simulate_automation_request_mock_item_outputs.additional_properties = additional_properties
        return simulate_automation_request_mock_item_outputs

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> list[Any]:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: list[Any]) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
