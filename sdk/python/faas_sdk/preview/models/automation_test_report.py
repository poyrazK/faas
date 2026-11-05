from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.automation_test_case_result import AutomationTestCaseResult


T = TypeVar("T", bound="AutomationTestReport")


@_attrs_define
class AutomationTestReport:
    """Retained observation, not a publishing receipt. Freshness compares definition and suite content; running does not
    advance the draft version or perform real actions.

    """

    checked_version: int
    checked_at: datetime.datetime
    inputs_hash: str
    passed: bool
    cases: list[AutomationTestCaseResult]

    def to_dict(self) -> dict[str, Any]:
        checked_version = self.checked_version

        checked_at = self.checked_at.isoformat()

        inputs_hash = self.inputs_hash

        passed = self.passed

        cases = []
        for cases_item_data in self.cases:
            cases_item = cases_item_data.to_dict()
            cases.append(cases_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "checked_version": checked_version,
                "checked_at": checked_at,
                "inputs_hash": inputs_hash,
                "passed": passed,
                "cases": cases,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.automation_test_case_result import AutomationTestCaseResult

        d = dict(src_dict)
        checked_version = d.pop("checked_version")

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        inputs_hash = d.pop("inputs_hash")

        passed = d.pop("passed")

        cases = []
        _cases = d.pop("cases")
        for cases_item_data in _cases:
            cases_item = AutomationTestCaseResult.from_dict(cases_item_data)

            cases.append(cases_item)

        automation_test_report = cls(
            checked_version=checked_version,
            checked_at=checked_at,
            inputs_hash=inputs_hash,
            passed=passed,
            cases=cases,
        )

        return automation_test_report
