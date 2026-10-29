from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_condition_refresh_progress_change_status import (
    OperationWorkflowConditionRefreshProgressChangeStatus,
    check_operation_workflow_condition_refresh_progress_change_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_decision_outcome_evidence import OperationWorkflowDecisionOutcomeEvidence


T = TypeVar("T", bound="OperationWorkflowConditionRefreshProgressChange")


@_attrs_define
class OperationWorkflowConditionRefreshProgressChange:
    """Before and optional after reports for one baseline overdue condition. Results may change while matching exact
    contract and decision/rule/version and condition ID/description. Newer report is independent of current freshness.
    Removed identities and budgets are not evidence of refreshed verification. Contract changes have no comparable after
    fact.

    """

    status: OperationWorkflowConditionRefreshProgressChangeStatus
    newer_report: bool
    before: OperationWorkflowDecisionOutcomeEvidence
    """Retained report classifying this contributor. Completed outcome groups use the earliest qualifying fact;
    active condition groups use the latest matching fact among current decision contexts. The optional business
    decision snapshot references its fact within operation_id. A path without a decision is evidenced by the report
    itself. These are application-reported facts and do not establish causation."""
    explanation: str
    after: OperationWorkflowDecisionOutcomeEvidence | Unset = UNSET
    """Retained report classifying this contributor. Completed outcome groups use the earliest qualifying fact;
    active condition groups use the latest matching fact among current decision contexts. The optional business
    decision snapshot references its fact within operation_id. A path without a decision is evidenced by the report
    itself. These are application-reported facts and do not establish causation."""

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        newer_report = self.newer_report

        before = self.before.to_dict()

        explanation = self.explanation

        after: dict[str, Any] | Unset = UNSET
        if not isinstance(self.after, Unset):
            after = self.after.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "status": status,
                "newer_report": newer_report,
                "before": before,
                "explanation": explanation,
            }
        )
        if after is not UNSET:
            field_dict["after"] = after

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_decision_outcome_evidence import OperationWorkflowDecisionOutcomeEvidence

        d = dict(src_dict)
        status = check_operation_workflow_condition_refresh_progress_change_status(d.pop("status"))

        newer_report = d.pop("newer_report")

        before = OperationWorkflowDecisionOutcomeEvidence.from_dict(d.pop("before"))

        explanation = d.pop("explanation")

        _after = d.pop("after", UNSET)
        after: OperationWorkflowDecisionOutcomeEvidence | Unset
        if isinstance(_after, Unset):
            after = UNSET
        else:
            after = OperationWorkflowDecisionOutcomeEvidence.from_dict(_after)

        operation_workflow_condition_refresh_progress_change = cls(
            status=status,
            newer_report=newer_report,
            before=before,
            explanation=explanation,
            after=after,
        )

        return operation_workflow_condition_refresh_progress_change
