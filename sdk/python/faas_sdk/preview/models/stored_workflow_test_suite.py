from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.stored_workflow_test_case import StoredWorkflowTestCase
    from ..models.stored_workflow_test_inputs import StoredWorkflowTestInputs


T = TypeVar("T", bound="StoredWorkflowTestSuite")


@_attrs_define
class StoredWorkflowTestSuite:
    """At most 32 uniquely named cases; sample up to 3 MiB, cases up to 24 MiB combined, suite up to 27 MiB. Data is
    explicitly persisted, never captured from real runs.

    """

    sample: StoredWorkflowTestInputs
    """Saved test data — Explicitly saved synthetic inputs and mocks. Runs always bind the current saved draft."""
    cases: list[StoredWorkflowTestCase]

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
        from ..models.stored_workflow_test_case import StoredWorkflowTestCase
        from ..models.stored_workflow_test_inputs import StoredWorkflowTestInputs

        d = dict(src_dict)
        sample = StoredWorkflowTestInputs.from_dict(d.pop("sample"))

        cases = []
        _cases = d.pop("cases")
        for cases_item_data in _cases:
            cases_item = StoredWorkflowTestCase.from_dict(cases_item_data)

            cases.append(cases_item)

        stored_workflow_test_suite = cls(
            sample=sample,
            cases=cases,
        )

        return stored_workflow_test_suite
