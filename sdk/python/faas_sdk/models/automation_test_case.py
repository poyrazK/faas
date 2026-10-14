from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.automation_check_expectation import AutomationCheckExpectation
    from ..models.automation_test_inputs import AutomationTestInputs


T = TypeVar("T", bound="AutomationTestCase")


@_attrs_define
class AutomationTestCase:
    """A uniquely named set of definition-free mocks and expected step results."""

    name: str
    simulation: AutomationTestInputs
    """Saved test data — Explicitly saved synthetic inputs and mocks. Runs always bind the current saved draft."""
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
        from ..models.automation_test_inputs import AutomationTestInputs

        d = dict(src_dict)
        name = d.pop("name")

        simulation = AutomationTestInputs.from_dict(d.pop("simulation"))

        expectations = []
        _expectations = d.pop("expectations")
        for expectations_item_data in _expectations:
            expectations_item = AutomationCheckExpectation.from_dict(expectations_item_data)

            expectations.append(expectations_item)

        automation_test_case = cls(
            name=name,
            simulation=simulation,
            expectations=expectations,
        )

        return automation_test_case
