from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_decision_outcome_evidence_observed_condition_results_item import (
    OperationWorkflowDecisionOutcomeEvidenceObservedConditionResultsItem,
    check_operation_workflow_decision_outcome_evidence_observed_condition_results_item,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_business_condition_result import OperationBusinessConditionResult
    from ..models.operation_business_decision import OperationBusinessDecision
    from ..models.operation_workflow_condition_comparison import OperationWorkflowConditionComparison
    from ..models.operation_workflow_condition_freshness import OperationWorkflowConditionFreshness


T = TypeVar("T", bound="OperationWorkflowDecisionOutcomeEvidence")


@_attrs_define
class OperationWorkflowDecisionOutcomeEvidence:
    """Retained report classifying this contributor. Completed outcome groups use the earliest qualifying fact; active
    condition groups use the latest matching fact among current decision contexts. The optional business decision
    snapshot references its fact within operation_id. A path without a decision is evidenced by the report itself. These
    are application-reported facts and do not establish causation.

    """

    report_id: UUID
    operation_id: UUID
    revision: int
    state: str
    occurred_at: datetime.datetime
    condition_freshness: OperationWorkflowConditionFreshness | Unset = UNSET
    """Report-age status using the selected fact reporting Operation's immutable condition definition and the
    summary evaluation time. A positive budget yields a refresh deadline from occurred_at; overdue begins at the
    deadline. Missing or zero budgets remain no_budget. This does not expire facts, change reported results, or
    authorize execution. Republishing an explicit snapshot updates reporting freshness; it does not prove renewed
    business verification."""
    evidence_age_seconds: int | Unset = UNSET
    """Age of the selected active condition fact at evaluated_at; not a condition expiry or assertion of current
    truth."""
    condition: OperationBusinessConditionResult | Unset = UNSET
    """Application-reported condition result matching the pinned milestone declaration. Omitted conditions remain
    unreported; unknown is explicit. No execution authority or causal inference."""
    observed_condition_results: list[OperationWorkflowDecisionOutcomeEvidenceObservedConditionResultsItem] | Unset = (
        UNSET
    )
    """All results reported before or at the first terminal report for the selected exact contract and decision and
    rule and condition ID/description identity. Present on condition contributors; multiple results explain
    overlapping group membership."""
    condition_comparison: OperationWorkflowConditionComparison | Unset = UNSET
    """Condition changes in explicit decision snapshots between consecutive retained revisions in the same tenant
    and workflow run and pinned contract. Shares history comparison statuses; missing decisions on metadata reports
    do not clear conditions. No causal inference."""
    from_state: str | Unset = UNSET
    milestone_id: UUID | Unset = UNSET
    decision: OperationBusinessDecision | Unset = UNSET
    """Application-reported business decision with a public explanation and optional exact policy rule identity."""

    def to_dict(self) -> dict[str, Any]:
        report_id = str(self.report_id)

        operation_id = str(self.operation_id)

        revision = self.revision

        state = self.state

        occurred_at = self.occurred_at.isoformat()

        condition_freshness: dict[str, Any] | Unset = UNSET
        if not isinstance(self.condition_freshness, Unset):
            condition_freshness = self.condition_freshness.to_dict()

        evidence_age_seconds = self.evidence_age_seconds

        condition: dict[str, Any] | Unset = UNSET
        if not isinstance(self.condition, Unset):
            condition = self.condition.to_dict()

        observed_condition_results: list[str] | Unset = UNSET
        if not isinstance(self.observed_condition_results, Unset):
            observed_condition_results = []
            for observed_condition_results_item_data in self.observed_condition_results:
                observed_condition_results_item: str = observed_condition_results_item_data
                observed_condition_results.append(observed_condition_results_item)

        condition_comparison: dict[str, Any] | Unset = UNSET
        if not isinstance(self.condition_comparison, Unset):
            condition_comparison = self.condition_comparison.to_dict()

        from_state = self.from_state

        milestone_id: str | Unset = UNSET
        if not isinstance(self.milestone_id, Unset):
            milestone_id = str(self.milestone_id)

        decision: dict[str, Any] | Unset = UNSET
        if not isinstance(self.decision, Unset):
            decision = self.decision.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "report_id": report_id,
                "operation_id": operation_id,
                "revision": revision,
                "state": state,
                "occurred_at": occurred_at,
            }
        )
        if condition_freshness is not UNSET:
            field_dict["condition_freshness"] = condition_freshness
        if evidence_age_seconds is not UNSET:
            field_dict["evidence_age_seconds"] = evidence_age_seconds
        if condition is not UNSET:
            field_dict["condition"] = condition
        if observed_condition_results is not UNSET:
            field_dict["observed_condition_results"] = observed_condition_results
        if condition_comparison is not UNSET:
            field_dict["condition_comparison"] = condition_comparison
        if from_state is not UNSET:
            field_dict["from_state"] = from_state
        if milestone_id is not UNSET:
            field_dict["milestone_id"] = milestone_id
        if decision is not UNSET:
            field_dict["decision"] = decision

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_business_condition_result import OperationBusinessConditionResult
        from ..models.operation_business_decision import OperationBusinessDecision
        from ..models.operation_workflow_condition_comparison import OperationWorkflowConditionComparison
        from ..models.operation_workflow_condition_freshness import OperationWorkflowConditionFreshness

        d = dict(src_dict)
        report_id = UUID(d.pop("report_id"))

        operation_id = UUID(d.pop("operation_id"))

        revision = d.pop("revision")

        state = d.pop("state")

        occurred_at = datetime.datetime.fromisoformat(d.pop("occurred_at"))

        _condition_freshness = d.pop("condition_freshness", UNSET)
        condition_freshness: OperationWorkflowConditionFreshness | Unset
        if isinstance(_condition_freshness, Unset):
            condition_freshness = UNSET
        else:
            condition_freshness = OperationWorkflowConditionFreshness.from_dict(_condition_freshness)

        evidence_age_seconds = d.pop("evidence_age_seconds", UNSET)

        _condition = d.pop("condition", UNSET)
        condition: OperationBusinessConditionResult | Unset
        if isinstance(_condition, Unset):
            condition = UNSET
        else:
            condition = OperationBusinessConditionResult.from_dict(_condition)

        _observed_condition_results = d.pop("observed_condition_results", UNSET)
        observed_condition_results: (
            list[OperationWorkflowDecisionOutcomeEvidenceObservedConditionResultsItem] | Unset
        ) = UNSET
        if _observed_condition_results is not UNSET:
            observed_condition_results = []
            for observed_condition_results_item_data in _observed_condition_results:
                observed_condition_results_item = (
                    check_operation_workflow_decision_outcome_evidence_observed_condition_results_item(
                        observed_condition_results_item_data
                    )
                )

                observed_condition_results.append(observed_condition_results_item)

        _condition_comparison = d.pop("condition_comparison", UNSET)
        condition_comparison: OperationWorkflowConditionComparison | Unset
        if isinstance(_condition_comparison, Unset):
            condition_comparison = UNSET
        else:
            condition_comparison = OperationWorkflowConditionComparison.from_dict(_condition_comparison)

        from_state = d.pop("from_state", UNSET)

        _milestone_id = d.pop("milestone_id", UNSET)
        milestone_id: UUID | Unset
        if isinstance(_milestone_id, Unset):
            milestone_id = UNSET
        else:
            milestone_id = UUID(_milestone_id)

        _decision = d.pop("decision", UNSET)
        decision: OperationBusinessDecision | Unset
        if isinstance(_decision, Unset):
            decision = UNSET
        else:
            decision = OperationBusinessDecision.from_dict(_decision)

        operation_workflow_decision_outcome_evidence = cls(
            report_id=report_id,
            operation_id=operation_id,
            revision=revision,
            state=state,
            occurred_at=occurred_at,
            condition_freshness=condition_freshness,
            evidence_age_seconds=evidence_age_seconds,
            condition=condition,
            observed_condition_results=observed_condition_results,
            condition_comparison=condition_comparison,
            from_state=from_state,
            milestone_id=milestone_id,
            decision=decision,
        )

        return operation_workflow_decision_outcome_evidence
