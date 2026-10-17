from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.automation_live_test_expected_run_status import (
    AutomationLiveTestExpectedRunStatus,
    check_automation_live_test_expected_run_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.automation_live_expectation import AutomationLiveExpectation


T = TypeVar("T", bound="AutomationLiveTest")


@_attrs_define
class AutomationLiveTest:
    """Explicit live assertions frozen when a saved case is admitted for execution."""

    expectations: list[AutomationLiveExpectation]
    expected_run_status: AutomationLiveTestExpectedRunStatus | Unset = UNSET
    """Desired terminal run state; omitted means succeeded. Cancellation always fails verification."""

    def to_dict(self) -> dict[str, Any]:
        expectations = []
        for expectations_item_data in self.expectations:
            expectations_item = expectations_item_data.to_dict()
            expectations.append(expectations_item)

        expected_run_status: str | Unset = UNSET
        if not isinstance(self.expected_run_status, Unset):
            expected_run_status = self.expected_run_status

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expectations": expectations,
            }
        )
        if expected_run_status is not UNSET:
            field_dict["expected_run_status"] = expected_run_status

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.automation_live_expectation import AutomationLiveExpectation

        d = dict(src_dict)
        expectations = []
        _expectations = d.pop("expectations")
        for expectations_item_data in _expectations:
            expectations_item = AutomationLiveExpectation.from_dict(expectations_item_data)

            expectations.append(expectations_item)

        _expected_run_status = d.pop("expected_run_status", UNSET)
        expected_run_status: AutomationLiveTestExpectedRunStatus | Unset
        if isinstance(_expected_run_status, Unset):
            expected_run_status = UNSET
        else:
            expected_run_status = check_automation_live_test_expected_run_status(_expected_run_status)

        automation_live_test = cls(
            expectations=expectations,
            expected_run_status=expected_run_status,
        )

        return automation_live_test
