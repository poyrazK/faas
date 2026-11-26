from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.api_sandbox_library_scenario_summary import APISandboxLibraryScenarioSummary


T = TypeVar("T", bound="APISandboxScenarioLibraryResponse")


@_attrs_define
class APISandboxScenarioLibraryResponse:
    scenarios: list[APISandboxLibraryScenarioSummary]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        scenarios = []
        for scenarios_item_data in self.scenarios:
            scenarios_item = scenarios_item_data.to_dict()
            scenarios.append(scenarios_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "scenarios": scenarios,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_sandbox_library_scenario_summary import APISandboxLibraryScenarioSummary

        d = dict(src_dict)
        scenarios = []
        _scenarios = d.pop("scenarios")
        for scenarios_item_data in _scenarios:
            scenarios_item = APISandboxLibraryScenarioSummary.from_dict(scenarios_item_data)

            scenarios.append(scenarios_item)

        api_sandbox_scenario_library_response = cls(
            scenarios=scenarios,
        )

        api_sandbox_scenario_library_response.additional_properties = d
        return api_sandbox_scenario_library_response

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
