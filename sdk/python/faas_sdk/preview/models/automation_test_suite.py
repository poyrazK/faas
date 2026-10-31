from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.automation_test_case import AutomationTestCase
    from ..models.automation_test_inputs import AutomationTestInputs


T = TypeVar("T", bound="AutomationTestSuite")


@_attrs_define
class AutomationTestSuite:
    """At most 32 uniquely named cases; sample up to 3 MiB, cases up to 24 MiB combined, suite up to 27 MiB. Data is
    explicitly persisted, never captured from real runs.

    """

    sample: AutomationTestInputs
    """Saved test data — Explicitly saved synthetic inputs and mocks. Runs always bind the current saved draft."""
    cases: list[AutomationTestCase]

    def to_dict(self) -> dict[str, Any]:
        sample = self.sample.to_dict()

        cases = []
        for cases_item_data in self.cases:
            cases_item = cases_item_data.to_dict()
            cases.append(cases_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "sample": sample,
                "cases": cases,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.automation_test_case import AutomationTestCase
        from ..models.automation_test_inputs import AutomationTestInputs

        d = dict(src_dict)
        sample = AutomationTestInputs.from_dict(d.pop("sample"))

        cases = []
        _cases = d.pop("cases")
        for cases_item_data in _cases:
            cases_item = AutomationTestCase.from_dict(cases_item_data)

            cases.append(cases_item)

        automation_test_suite = cls(
            sample=sample,
            cases=cases,
        )

        return automation_test_suite
