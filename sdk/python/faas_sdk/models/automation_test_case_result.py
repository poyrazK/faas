from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.automation_test_case_result_issue import (
    AutomationTestCaseResultIssue,
    check_automation_test_case_result_issue,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.automation_test_step_result import AutomationTestStepResult


T = TypeVar("T", bound="AutomationTestCaseResult")


@_attrs_define
class AutomationTestCaseResult:
    """Safe overall case result and bounded per-step assertion summaries."""

    name: str
    passed: bool
    steps: list[AutomationTestStepResult]
    issue: AutomationTestCaseResultIssue | Unset = UNSET
    steps_truncated: bool | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        passed = self.passed

        steps = []
        for steps_item_data in self.steps:
            steps_item = steps_item_data.to_dict()
            steps.append(steps_item)

        issue: str | Unset = UNSET
        if not isinstance(self.issue, Unset):
            issue = self.issue

        steps_truncated = self.steps_truncated

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "passed": passed,
                "steps": steps,
            }
        )
        if issue is not UNSET:
            field_dict["issue"] = issue
        if steps_truncated is not UNSET:
            field_dict["steps_truncated"] = steps_truncated

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.automation_test_step_result import AutomationTestStepResult

        d = dict(src_dict)
        name = d.pop("name")

        passed = d.pop("passed")

        steps = []
        _steps = d.pop("steps")
        for steps_item_data in _steps:
            steps_item = AutomationTestStepResult.from_dict(steps_item_data)

            steps.append(steps_item)

        _issue = d.pop("issue", UNSET)
        issue: AutomationTestCaseResultIssue | Unset
        if isinstance(_issue, Unset):
            issue = UNSET
        else:
            issue = check_automation_test_case_result_issue(_issue)

        steps_truncated = d.pop("steps_truncated", UNSET)

        automation_test_case_result = cls(
            name=name,
            passed=passed,
            steps=steps,
            issue=issue,
            steps_truncated=steps_truncated,
        )

        return automation_test_case_result
