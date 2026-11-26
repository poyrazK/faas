from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.api_sandbox_approval_checks_response_availability import (
    APISandboxApprovalChecksResponseAvailability,
    check_api_sandbox_approval_checks_response_availability,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.api_sandbox_approval_check import APISandboxApprovalCheck
    from ..models.api_sandbox_library_scenario_summary import APISandboxLibraryScenarioSummary


T = TypeVar("T", bound="APISandboxApprovalChecksResponse")


@_attrs_define
class APISandboxApprovalChecksResponse:
    """Prior approval checks and inputs available for a new check."""

    availability: APISandboxApprovalChecksResponseAvailability
    checks: list[APISandboxApprovalCheck]
    baseline_deployment_id: UUID | Unset = UNSET
    scenario_revision: UUID | Unset = UNSET
    scenario_name: str | Unset = UNSET
    library_scenarios: list[APISandboxLibraryScenarioSummary] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        availability: str = self.availability

        checks = []
        for checks_item_data in self.checks:
            checks_item = checks_item_data.to_dict()
            checks.append(checks_item)

        baseline_deployment_id: str | Unset = UNSET
        if not isinstance(self.baseline_deployment_id, Unset):
            baseline_deployment_id = str(self.baseline_deployment_id)

        scenario_revision: str | Unset = UNSET
        if not isinstance(self.scenario_revision, Unset):
            scenario_revision = str(self.scenario_revision)

        scenario_name = self.scenario_name

        library_scenarios: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.library_scenarios, Unset):
            library_scenarios = []
            for library_scenarios_item_data in self.library_scenarios:
                library_scenarios_item = library_scenarios_item_data.to_dict()
                library_scenarios.append(library_scenarios_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "availability": availability,
                "checks": checks,
            }
        )
        if baseline_deployment_id is not UNSET:
            field_dict["baseline_deployment_id"] = baseline_deployment_id
        if scenario_revision is not UNSET:
            field_dict["scenario_revision"] = scenario_revision
        if scenario_name is not UNSET:
            field_dict["scenario_name"] = scenario_name
        if library_scenarios is not UNSET:
            field_dict["library_scenarios"] = library_scenarios

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_sandbox_approval_check import APISandboxApprovalCheck
        from ..models.api_sandbox_library_scenario_summary import APISandboxLibraryScenarioSummary

        d = dict(src_dict)
        availability = check_api_sandbox_approval_checks_response_availability(d.pop("availability"))

        checks = []
        _checks = d.pop("checks")
        for checks_item_data in _checks:
            checks_item = APISandboxApprovalCheck.from_dict(checks_item_data)

            checks.append(checks_item)

        _baseline_deployment_id = d.pop("baseline_deployment_id", UNSET)
        baseline_deployment_id: UUID | Unset
        if isinstance(_baseline_deployment_id, Unset):
            baseline_deployment_id = UNSET
        else:
            baseline_deployment_id = UUID(_baseline_deployment_id)

        _scenario_revision = d.pop("scenario_revision", UNSET)
        scenario_revision: UUID | Unset
        if isinstance(_scenario_revision, Unset):
            scenario_revision = UNSET
        else:
            scenario_revision = UUID(_scenario_revision)

        scenario_name = d.pop("scenario_name", UNSET)

        _library_scenarios = d.pop("library_scenarios", UNSET)
        library_scenarios: list[APISandboxLibraryScenarioSummary] | Unset = UNSET
        if _library_scenarios is not UNSET:
            library_scenarios = []
            for library_scenarios_item_data in _library_scenarios:
                library_scenarios_item = APISandboxLibraryScenarioSummary.from_dict(library_scenarios_item_data)

                library_scenarios.append(library_scenarios_item)

        api_sandbox_approval_checks_response = cls(
            availability=availability,
            checks=checks,
            baseline_deployment_id=baseline_deployment_id,
            scenario_revision=scenario_revision,
            scenario_name=scenario_name,
            library_scenarios=library_scenarios,
        )

        api_sandbox_approval_checks_response.additional_properties = d
        return api_sandbox_approval_checks_response

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
