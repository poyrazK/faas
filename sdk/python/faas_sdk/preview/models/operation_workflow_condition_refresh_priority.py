from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_decision_outcome_evidence import OperationWorkflowDecisionOutcomeEvidence


T = TypeVar("T", bound="OperationWorkflowConditionRefreshPriority")


@_attrs_define
class OperationWorkflowConditionRefreshPriority:
    """Evidence and explanation for one ongoing workflow with overdue condition reports. Rankings use the same token-bound
    sample, complete-history eligibility and pinned definitions as active condition coverage. Owner lists and unassigned
    condition counts include all qualifying identities before the 16-fact preview cap. The oldest evidence has the
    earliest selected overdue report timestamp with canonical identity breaking ties, and is included independently of
    the preview. This is observational prioritization without business severity or execution authority.

    """

    oldest_evidence: OperationWorkflowDecisionOutcomeEvidence
    """Retained report classifying this contributor. Completed outcome groups use the earliest qualifying fact;
    active condition groups use the latest matching fact among current decision contexts. The optional business
    decision snapshot references its fact within operation_id. A path without a decision is evidenced by the report
    itself. These are application-reported facts and do not establish causation."""
    oldest_evidence_age_seconds: int
    """Age of the oldest selected overdue report at evaluated_at. This is report age rather than seconds past its
    refresh deadline."""
    blocked_seconds: int
    """Full observed workflow blocked duration through evaluated_at; not time attributed to these conditions."""
    refresh_owners: list[str]
    """All distinct assigned refresh owners across qualifying overdue conditions, sorted lexically. Independent of
    owner-group display and evidence preview limits."""
    unassigned_condition_count: int
    """Number of qualifying overdue conditions without a pinned refresh owner; independent of preview limits."""
    explanation: str
    """Explains the selected age or blocked-duration ranking using observed evidence without assigning causation or
    severity."""

    def to_dict(self) -> dict[str, Any]:
        oldest_evidence = self.oldest_evidence.to_dict()

        oldest_evidence_age_seconds = self.oldest_evidence_age_seconds

        blocked_seconds = self.blocked_seconds

        refresh_owners = self.refresh_owners

        unassigned_condition_count = self.unassigned_condition_count

        explanation = self.explanation

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "oldest_evidence": oldest_evidence,
                "oldest_evidence_age_seconds": oldest_evidence_age_seconds,
                "blocked_seconds": blocked_seconds,
                "refresh_owners": refresh_owners,
                "unassigned_condition_count": unassigned_condition_count,
                "explanation": explanation,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_decision_outcome_evidence import OperationWorkflowDecisionOutcomeEvidence

        d = dict(src_dict)
        oldest_evidence = OperationWorkflowDecisionOutcomeEvidence.from_dict(d.pop("oldest_evidence"))

        oldest_evidence_age_seconds = d.pop("oldest_evidence_age_seconds")

        blocked_seconds = d.pop("blocked_seconds")

        refresh_owners = cast(list[str], d.pop("refresh_owners"))

        unassigned_condition_count = d.pop("unassigned_condition_count")

        explanation = d.pop("explanation")

        operation_workflow_condition_refresh_priority = cls(
            oldest_evidence=oldest_evidence,
            oldest_evidence_age_seconds=oldest_evidence_age_seconds,
            blocked_seconds=blocked_seconds,
            refresh_owners=refresh_owners,
            unassigned_condition_count=unassigned_condition_count,
            explanation=explanation,
        )

        return operation_workflow_condition_refresh_priority
