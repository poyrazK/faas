from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.api_sandbox_suite_scenario_result_status import (
    APISandboxSuiteScenarioResultStatus,
    check_api_sandbox_suite_scenario_result_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.api_sandbox_comparison_report import APISandboxComparisonReport


T = TypeVar("T", bound="APISandboxSuiteScenarioResult")


@_attrs_define
class APISandboxSuiteScenarioResult:
    scenario_id: UUID
    scenario_name: str
    scenario_revision: UUID
    status: APISandboxSuiteScenarioResultStatus
    comparison: APISandboxComparisonReport | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        scenario_id = str(self.scenario_id)

        scenario_name = self.scenario_name

        scenario_revision = str(self.scenario_revision)

        status: str = self.status

        comparison: dict[str, Any] | Unset = UNSET
        if not isinstance(self.comparison, Unset):
            comparison = self.comparison.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "scenario_id": scenario_id,
                "scenario_name": scenario_name,
                "scenario_revision": scenario_revision,
                "status": status,
            }
        )
        if comparison is not UNSET:
            field_dict["comparison"] = comparison

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_sandbox_comparison_report import APISandboxComparisonReport

        d = dict(src_dict)
        scenario_id = UUID(d.pop("scenario_id"))

        scenario_name = d.pop("scenario_name")

        scenario_revision = UUID(d.pop("scenario_revision"))

        status = check_api_sandbox_suite_scenario_result_status(d.pop("status"))

        _comparison = d.pop("comparison", UNSET)
        comparison: APISandboxComparisonReport | Unset
        if isinstance(_comparison, Unset):
            comparison = UNSET
        else:
            comparison = APISandboxComparisonReport.from_dict(_comparison)

        api_sandbox_suite_scenario_result = cls(
            scenario_id=scenario_id,
            scenario_name=scenario_name,
            scenario_revision=scenario_revision,
            status=status,
            comparison=comparison,
        )

        api_sandbox_suite_scenario_result.additional_properties = d
        return api_sandbox_suite_scenario_result

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
