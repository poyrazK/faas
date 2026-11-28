from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_missing_condition_progress_change_status import (
    OperationWorkflowMissingConditionProgressChangeStatus,
    check_operation_workflow_missing_condition_progress_change_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_condition_coverage_entry import OperationWorkflowConditionCoverageEntry


T = TypeVar("T", bound="OperationWorkflowMissingConditionProgressChange")


@_attrs_define
class OperationWorkflowMissingConditionProgressChange:
    """Before is a baseline missing declaration without evidence. After is an optional current expectation and its selected
    result/freshness. Match step and condition ID to locate replacements, but receiving a report requires identical
    pinned declaration and milestone. Changed vocabulary, expected states, budgets or guidance are expectation_changed.
    Absent current expectations are not_expected; no cross-contract after comparison is emitted. Missing reports and no-
    longer-expected conditions are not failed or passed business verification.

    """

    status: OperationWorkflowMissingConditionProgressChangeStatus
    before: OperationWorkflowConditionCoverageEntry
    """One condition expected in the current state. Match the declaring workflow step and exact condition ID and
    description within the current contract and latest explicit contexts. Evidence keeps the source report pinned
    age budget and decision identity. Reported includes any application result and may have no freshness budget;
    overdue is evidence past its budget. Missing has no evidence. This grants no authorization or verification."""
    explanation: str
    after: OperationWorkflowConditionCoverageEntry | Unset = UNSET
    """One condition expected in the current state. Match the declaring workflow step and exact condition ID and
    description within the current contract and latest explicit contexts. Evidence keeps the source report pinned
    age budget and decision identity. Reported includes any application result and may have no freshness budget;
    overdue is evidence past its budget. Missing has no evidence. This grants no authorization or verification."""

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        before = self.before.to_dict()

        explanation = self.explanation

        after: dict[str, Any] | Unset = UNSET
        if not isinstance(self.after, Unset):
            after = self.after.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "status": status,
                "before": before,
                "explanation": explanation,
            }
        )
        if after is not UNSET:
            field_dict["after"] = after

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_condition_coverage_entry import OperationWorkflowConditionCoverageEntry

        d = dict(src_dict)
        status = check_operation_workflow_missing_condition_progress_change_status(d.pop("status"))

        before = OperationWorkflowConditionCoverageEntry.from_dict(d.pop("before"))

        explanation = d.pop("explanation")

        _after = d.pop("after", UNSET)
        after: OperationWorkflowConditionCoverageEntry | Unset
        if isinstance(_after, Unset):
            after = UNSET
        else:
            after = OperationWorkflowConditionCoverageEntry.from_dict(_after)

        operation_workflow_missing_condition_progress_change = cls(
            status=status,
            before=before,
            explanation=explanation,
            after=after,
        )

        return operation_workflow_missing_condition_progress_change
