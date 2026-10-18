from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.automation_test_step_result import AutomationTestStepResult


T = TypeVar("T", bound="AutomationLiveReport")


@_attrs_define
class AutomationLiveReport:
    """Payload-free verdict for the frozen live assertions at one terminal run state."""

    checked_at: datetime.datetime
    passed: bool
    run_status: str
    resume_count: int
    run_updated_at: datetime.datetime
    steps: list[AutomationTestStepResult]
    issue: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        checked_at = self.checked_at.isoformat()

        passed = self.passed

        run_status = self.run_status

        resume_count = self.resume_count

        run_updated_at = self.run_updated_at.isoformat()

        steps = []
        for steps_item_data in self.steps:
            steps_item = steps_item_data.to_dict()
            steps.append(steps_item)

        issue = self.issue

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "checked_at": checked_at,
                "passed": passed,
                "run_status": run_status,
                "resume_count": resume_count,
                "run_updated_at": run_updated_at,
                "steps": steps,
            }
        )
        if issue is not UNSET:
            field_dict["issue"] = issue

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.automation_test_step_result import AutomationTestStepResult

        d = dict(src_dict)
        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        passed = d.pop("passed")

        run_status = d.pop("run_status")

        resume_count = d.pop("resume_count")

        run_updated_at = datetime.datetime.fromisoformat(d.pop("run_updated_at"))

        steps = []
        _steps = d.pop("steps")
        for steps_item_data in _steps:
            steps_item = AutomationTestStepResult.from_dict(steps_item_data)

            steps.append(steps_item)

        issue = d.pop("issue", UNSET)

        automation_live_report = cls(
            checked_at=checked_at,
            passed=passed,
            run_status=run_status,
            resume_count=resume_count,
            run_updated_at=run_updated_at,
            steps=steps,
            issue=issue,
        )

        return automation_live_report
