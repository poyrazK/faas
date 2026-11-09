from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.stored_workflow_live_test_expected_run_status import (
    StoredWorkflowLiveTestExpectedRunStatus,
    check_stored_workflow_live_test_expected_run_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.stored_workflow_live_expectation import StoredWorkflowLiveExpectation


T = TypeVar("T", bound="StoredWorkflowLiveTest")


@_attrs_define
class StoredWorkflowLiveTest:
    """Explicit live assertions frozen when a saved case is admitted for execution."""

    expectations: list[StoredWorkflowLiveExpectation]
    expected_run_status: StoredWorkflowLiveTestExpectedRunStatus | Unset = UNSET
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
        from ..models.stored_workflow_live_expectation import StoredWorkflowLiveExpectation

        d = dict(src_dict)
        expectations = []
        _expectations = d.pop("expectations")
        for expectations_item_data in _expectations:
            expectations_item = StoredWorkflowLiveExpectation.from_dict(expectations_item_data)

            expectations.append(expectations_item)

        _expected_run_status = d.pop("expected_run_status", UNSET)
        expected_run_status: StoredWorkflowLiveTestExpectedRunStatus | Unset
        if isinstance(_expected_run_status, Unset):
            expected_run_status = UNSET
        else:
            expected_run_status = check_stored_workflow_live_test_expected_run_status(_expected_run_status)

        stored_workflow_live_test = cls(
            expectations=expectations,
            expected_run_status=expected_run_status,
        )

        return stored_workflow_live_test
