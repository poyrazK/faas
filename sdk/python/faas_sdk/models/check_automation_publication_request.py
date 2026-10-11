from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.automation_check_exclusion import AutomationCheckExclusion
    from ..models.automation_publish_check_scenario import AutomationPublishCheckScenario


T = TypeVar("T", bound="CheckAutomationPublicationRequest")


@_attrs_define
class CheckAutomationPublicationRequest:
    """Run assertions and coverage on the saved draft on the server; mocks remain hypothetical. At most 24 MiB total and 3
    MiB per simulation. Complete, valid traces and passing assertions are required. Coverage policy overrides
    require_coverage=false.

    """

    expected_version: int
    scenarios: list[AutomationPublishCheckScenario]
    exclusions: list[AutomationCheckExclusion]
    require_coverage: bool | Unset = False

    def to_dict(self) -> dict[str, Any]:
        expected_version = self.expected_version

        scenarios = []
        for scenarios_item_data in self.scenarios:
            scenarios_item = scenarios_item_data.to_dict()
            scenarios.append(scenarios_item)

        exclusions = []
        for exclusions_item_data in self.exclusions:
            exclusions_item = exclusions_item_data.to_dict()
            exclusions.append(exclusions_item)

        require_coverage = self.require_coverage

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_version": expected_version,
                "scenarios": scenarios,
                "exclusions": exclusions,
            }
        )
        if require_coverage is not UNSET:
            field_dict["require_coverage"] = require_coverage

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.automation_check_exclusion import AutomationCheckExclusion
        from ..models.automation_publish_check_scenario import AutomationPublishCheckScenario

        d = dict(src_dict)
        expected_version = d.pop("expected_version")

        scenarios = []
        _scenarios = d.pop("scenarios")
        for scenarios_item_data in _scenarios:
            scenarios_item = AutomationPublishCheckScenario.from_dict(scenarios_item_data)

            scenarios.append(scenarios_item)

        exclusions = []
        _exclusions = d.pop("exclusions")
        for exclusions_item_data in _exclusions:
            exclusions_item = AutomationCheckExclusion.from_dict(exclusions_item_data)

            exclusions.append(exclusions_item)

        require_coverage = d.pop("require_coverage", UNSET)

        check_automation_publication_request = cls(
            expected_version=expected_version,
            scenarios=scenarios,
            exclusions=exclusions,
            require_coverage=require_coverage,
        )

        return check_automation_publication_request
