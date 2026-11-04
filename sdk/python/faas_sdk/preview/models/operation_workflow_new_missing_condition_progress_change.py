from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_new_missing_condition_progress_change_status import (
    OperationWorkflowNewMissingConditionProgressChangeStatus,
    check_operation_workflow_new_missing_condition_progress_change_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_condition_coverage_entry import OperationWorkflowConditionCoverageEntry


T = TypeVar("T", bound="OperationWorkflowNewMissingConditionProgressChange")


@_attrs_define
class OperationWorkflowNewMissingConditionProgressChange:
    """After is a current missing expectation without evidence. Before optionally includes prior pinned declaration and
    evidence with result and baseline freshness. Same step and condition ID locate candidates; report_lost requires
    identical milestone and full declaration including expected states, budget and guidance plus prior evidence. Age
    expiry never removes reports. Changed declarations are expectation_changed; absent or previously inactive terminal
    expectations are new_expectation. Contract changes omit Before and never compare facts across contracts.

    """

    status: OperationWorkflowNewMissingConditionProgressChangeStatus
    after: OperationWorkflowConditionCoverageEntry
    """One condition expected in the current state. Match the declaring workflow step and exact condition ID and
    description within the current contract and latest explicit contexts. Evidence keeps the source report pinned
    age budget and decision identity. Reported includes any application result and may have no freshness budget;
    overdue is evidence past its budget. Missing has no evidence. This grants no authorization or verification."""
    explanation: str
    before: OperationWorkflowConditionCoverageEntry | Unset = UNSET
    """One condition expected in the current state. Match the declaring workflow step and exact condition ID and
    description within the current contract and latest explicit contexts. Evidence keeps the source report pinned
    age budget and decision identity. Reported includes any application result and may have no freshness budget;
    overdue is evidence past its budget. Missing has no evidence. This grants no authorization or verification."""

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        after = self.after.to_dict()

        explanation = self.explanation

        before: dict[str, Any] | Unset = UNSET
        if not isinstance(self.before, Unset):
            before = self.before.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "status": status,
                "after": after,
                "explanation": explanation,
            }
        )
        if before is not UNSET:
            field_dict["before"] = before

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_condition_coverage_entry import OperationWorkflowConditionCoverageEntry

        d = dict(src_dict)
        status = check_operation_workflow_new_missing_condition_progress_change_status(d.pop("status"))

        after = OperationWorkflowConditionCoverageEntry.from_dict(d.pop("after"))

        explanation = d.pop("explanation")

        _before = d.pop("before", UNSET)
        before: OperationWorkflowConditionCoverageEntry | Unset
        if isinstance(_before, Unset):
            before = UNSET
        else:
            before = OperationWorkflowConditionCoverageEntry.from_dict(_before)

        operation_workflow_new_missing_condition_progress_change = cls(
            status=status,
            after=after,
            explanation=explanation,
            before=before,
        )

        return operation_workflow_new_missing_condition_progress_change
