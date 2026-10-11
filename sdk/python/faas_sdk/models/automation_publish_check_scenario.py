from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.automation_check_expectation import AutomationCheckExpectation
    from ..models.simulate_automation_request import SimulateAutomationRequest


T = TypeVar("T", bound="AutomationPublishCheckScenario")


@_attrs_define
class AutomationPublishCheckScenario:
    """Named simulation and assertions to evaluate against a saved automation draft."""

    name: str
    simulation: SimulateAutomationRequest
    """Sample workflow data and mocked action, event/callback payload or timeout outcomes for a stateless
    simulation."""
    expectations: list[AutomationCheckExpectation]

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        simulation = self.simulation.to_dict()

        expectations = []
        for expectations_item_data in self.expectations:
            expectations_item = expectations_item_data.to_dict()
            expectations.append(expectations_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "simulation": simulation,
                "expectations": expectations,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.automation_check_expectation import AutomationCheckExpectation
        from ..models.simulate_automation_request import SimulateAutomationRequest

        d = dict(src_dict)
        name = d.pop("name")

        simulation = SimulateAutomationRequest.from_dict(d.pop("simulation"))

        expectations = []
        _expectations = d.pop("expectations")
        for expectations_item_data in _expectations:
            expectations_item = AutomationCheckExpectation.from_dict(expectations_item_data)

            expectations.append(expectations_item)

        automation_publish_check_scenario = cls(
            name=name,
            simulation=simulation,
            expectations=expectations,
        )

        return automation_publish_check_scenario
