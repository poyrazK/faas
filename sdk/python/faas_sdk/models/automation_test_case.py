from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.automation_check_expectation import AutomationCheckExpectation
    from ..models.automation_live_test import AutomationLiveTest
    from ..models.automation_test_inputs import AutomationTestInputs


T = TypeVar("T", bound="AutomationTestCase")


@_attrs_define
class AutomationTestCase:
    """A uniquely named set of definition-free mocks and expected step results."""

    name: str
    simulation: AutomationTestInputs
    """Saved test data — Explicitly saved synthetic inputs and mocks. Runs always bind the current saved draft."""
    expectations: list[AutomationCheckExpectation]
    live: AutomationLiveTest | None | Unset = UNSET
    """Optional live assertions saved with this case; null disables its live test."""

    def to_dict(self) -> dict[str, Any]:
        from ..models.automation_live_test import AutomationLiveTest

        name = self.name

        simulation = self.simulation.to_dict()

        expectations = []
        for expectations_item_data in self.expectations:
            expectations_item = expectations_item_data.to_dict()
            expectations.append(expectations_item)

        live: dict[str, Any] | None | Unset
        if isinstance(self.live, Unset):
            live = UNSET
        elif isinstance(self.live, AutomationLiveTest):
            live = self.live.to_dict()
        else:
            live = self.live

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "simulation": simulation,
                "expectations": expectations,
            }
        )
        if live is not UNSET:
            field_dict["live"] = live

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.automation_check_expectation import AutomationCheckExpectation
        from ..models.automation_live_test import AutomationLiveTest
        from ..models.automation_test_inputs import AutomationTestInputs

        d = dict(src_dict)
        name = d.pop("name")

        simulation = AutomationTestInputs.from_dict(d.pop("simulation"))

        expectations = []
        _expectations = d.pop("expectations")
        for expectations_item_data in _expectations:
            expectations_item = AutomationCheckExpectation.from_dict(expectations_item_data)

            expectations.append(expectations_item)

        def _parse_live(data: object) -> AutomationLiveTest | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                live_type_0 = AutomationLiveTest.from_dict(data)

                return live_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(AutomationLiveTest | None | Unset, data)

        live = _parse_live(d.pop("live", UNSET))

        automation_test_case = cls(
            name=name,
            simulation=simulation,
            expectations=expectations,
            live=live,
        )

        return automation_test_case
