from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.stored_workflow_test_suite import StoredWorkflowTestSuite


T = TypeVar("T", bound="SaveStoredWorkflowTestSuiteRequest")


@_attrs_define
class SaveStoredWorkflowTestSuiteRequest:
    """Versioned suite replacement; explicitly sending null removes saved data and results."""

    expected_version: int
    suite: None | StoredWorkflowTestSuite
    """Null clears saved tests and the retained report. Saving advances the stored workflow version and invalidates
    earlier publishing receipts."""

    def to_dict(self) -> dict[str, Any]:
        from ..models.stored_workflow_test_suite import StoredWorkflowTestSuite

        expected_version = self.expected_version

        suite: dict[str, Any] | None
        if isinstance(self.suite, StoredWorkflowTestSuite):
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
        from ..models.stored_workflow_test_suite import StoredWorkflowTestSuite

        d = dict(src_dict)
        expected_version = d.pop("expected_version")

        def _parse_suite(data: object) -> None | StoredWorkflowTestSuite:
            if data is None:
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                suite_type_0 = StoredWorkflowTestSuite.from_dict(data)

                return suite_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | StoredWorkflowTestSuite, data)

        suite = _parse_suite(d.pop("suite"))

        save_stored_workflow_test_suite_request = cls(
            expected_version=expected_version,
            suite=suite,
        )

        return save_stored_workflow_test_suite_request
