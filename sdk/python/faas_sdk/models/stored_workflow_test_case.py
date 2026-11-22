from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.stored_workflow_check_expectation import StoredWorkflowCheckExpectation
    from ..models.stored_workflow_live_test import StoredWorkflowLiveTest
    from ..models.stored_workflow_test_inputs import StoredWorkflowTestInputs


T = TypeVar("T", bound="StoredWorkflowTestCase")


@_attrs_define
class StoredWorkflowTestCase:
    """A uniquely named set of definition-free mocks and expected step results."""

    name: str
    simulation: StoredWorkflowTestInputs
    """Saved test data — Explicitly saved synthetic inputs and mocks. Runs always bind the current saved draft."""
    expectations: list[StoredWorkflowCheckExpectation]
    live: None | StoredWorkflowLiveTest | Unset = UNSET
    """Optional live assertions saved with this case; null disables its live test."""

    def to_dict(self) -> dict[str, Any]:
        from ..models.stored_workflow_live_test import StoredWorkflowLiveTest

        name = self.name

        simulation = self.simulation.to_dict()

        expectations = []
        for expectations_item_data in self.expectations:
            expectations_item = expectations_item_data.to_dict()
            expectations.append(expectations_item)

        live: dict[str, Any] | None | Unset
        if isinstance(self.live, Unset):
            live = UNSET
        elif isinstance(self.live, StoredWorkflowLiveTest):
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
        from ..models.stored_workflow_check_expectation import StoredWorkflowCheckExpectation
        from ..models.stored_workflow_live_test import StoredWorkflowLiveTest
        from ..models.stored_workflow_test_inputs import StoredWorkflowTestInputs

        d = dict(src_dict)
        name = d.pop("name")

        simulation = StoredWorkflowTestInputs.from_dict(d.pop("simulation"))

        expectations = []
        _expectations = d.pop("expectations")
        for expectations_item_data in _expectations:
            expectations_item = StoredWorkflowCheckExpectation.from_dict(expectations_item_data)

            expectations.append(expectations_item)

        def _parse_live(data: object) -> None | StoredWorkflowLiveTest | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                live_type_0 = StoredWorkflowLiveTest.from_dict(data)

                return live_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | StoredWorkflowLiveTest | Unset, data)

        live = _parse_live(d.pop("live", UNSET))

        stored_workflow_test_case = cls(
            name=name,
            simulation=simulation,
            expectations=expectations,
            live=live,
        )

        return stored_workflow_test_case
