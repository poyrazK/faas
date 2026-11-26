from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.api_sandbox_comparison_baseline import APISandboxComparisonBaseline
    from ..models.api_sandbox_comparison_report import APISandboxComparisonReport


T = TypeVar("T", bound="APISandboxComparisonsResponse")


@_attrs_define
class APISandboxComparisonsResponse:
    comparisons: list[APISandboxComparisonReport]
    baselines: list[APISandboxComparisonBaseline]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        comparisons = []
        for comparisons_item_data in self.comparisons:
            comparisons_item = comparisons_item_data.to_dict()
            comparisons.append(comparisons_item)

        baselines = []
        for baselines_item_data in self.baselines:
            baselines_item = baselines_item_data.to_dict()
            baselines.append(baselines_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "comparisons": comparisons,
                "baselines": baselines,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_sandbox_comparison_baseline import APISandboxComparisonBaseline
        from ..models.api_sandbox_comparison_report import APISandboxComparisonReport

        d = dict(src_dict)
        comparisons = []
        _comparisons = d.pop("comparisons")
        for comparisons_item_data in _comparisons:
            comparisons_item = APISandboxComparisonReport.from_dict(comparisons_item_data)

            comparisons.append(comparisons_item)

        baselines = []
        _baselines = d.pop("baselines")
        for baselines_item_data in _baselines:
            baselines_item = APISandboxComparisonBaseline.from_dict(baselines_item_data)

            baselines.append(baselines_item)

        api_sandbox_comparisons_response = cls(
            comparisons=comparisons,
            baselines=baselines,
        )

        api_sandbox_comparisons_response.additional_properties = d
        return api_sandbox_comparisons_response

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
