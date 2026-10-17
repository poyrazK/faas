from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.automation_test_suite import AutomationTestSuite


T = TypeVar("T", bound="SaveAutomationTestSuiteRequest")


@_attrs_define
class SaveAutomationTestSuiteRequest:
    """Versioned suite replacement; explicitly sending null removes saved data and results."""

    expected_version: int
    suite: AutomationTestSuite | None
    """Null clears saved tests and the retained report. Saving advances the automation version and invalidates
    earlier publishing receipts."""

    def to_dict(self) -> dict[str, Any]:
        from ..models.automation_test_suite import AutomationTestSuite

        expected_version = self.expected_version

        suite: dict[str, Any] | None
        if isinstance(self.suite, AutomationTestSuite):
            suite = self.suite.to_dict()
        else:
            suite = self.suite

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_version": expected_version,
                "suite": suite,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.automation_test_suite import AutomationTestSuite

        d = dict(src_dict)
        expected_version = d.pop("expected_version")

        def _parse_suite(data: object) -> AutomationTestSuite | None:
            if data is None:
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                suite_type_0 = AutomationTestSuite.from_dict(data)

                return suite_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(AutomationTestSuite | None, data)

        suite = _parse_suite(d.pop("suite"))

        save_automation_test_suite_request = cls(
            expected_version=expected_version,
            suite=suite,
        )

        return save_automation_test_suite_request
