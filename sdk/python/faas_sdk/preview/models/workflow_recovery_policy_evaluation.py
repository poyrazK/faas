from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.workflow_recovery_failure_coverage import WorkflowRecoveryFailureCoverage
    from ..models.workflow_recovery_policy import WorkflowRecoveryPolicy
    from ..models.workflow_recovery_policy_evaluation_candidate import WorkflowRecoveryPolicyEvaluationCandidate
    from ..models.workflow_recovery_policy_evaluation_totals import WorkflowRecoveryPolicyEvaluationTotals
    from ..models.workflow_recovery_policy_response import WorkflowRecoveryPolicyResponse


T = TypeVar("T", bound="WorkflowRecoveryPolicyEvaluation")


@_attrs_define
class WorkflowRecoveryPolicyEvaluation:
    """Historical comparison under stated replay assumptions, with retained-evidence gaps and every bounded sample
    truncation disclosed.

    """

    failure_coverage: WorkflowRecoveryFailureCoverage
    """Coverage of the bounded policy-independent terminal-failure sample in the requested workflow/time window,
    before policy filters. Counts are sample counts; partial capture and pruning never establish that no other
    failures occurred. Review correlation uses the sampled reviews."""
    current: WorkflowRecoveryPolicyResponse
    """Current recovery policy with trusted update actor and scheduler status. A versioned policy replacement
    resets its automatic failure pause and suppresses older pending automatic actions."""
    proposed: WorkflowRecoveryPolicy
    """Explicit app recovery policy. Preview prepares parent-job plans and worker-discovered initial-failure
    reviews. initial_failure requires explicit failure statuses. Opt-in automatic_retry requires saved_failure only;
    automatic_initial requires initial_failure only. Both require explicit workflow and failure-status filters and
    execution limits. Resume count caps the absolute generation; preparation and execution budgets are separate.
    Cancellation remains manual."""
    observed_at: datetime.datetime
    created_after: datetime.datetime
    created_before: datetime.datetime
    inspected_reviews: int
    reviews_truncated: bool
    candidates_truncated: bool
    executions_truncated: bool
    observed_executions: int
    current_totals: WorkflowRecoveryPolicyEvaluationTotals
    """Counts of conditional preparation decisions and observed application pressure for the sampled candidates."""
    proposed_totals: WorkflowRecoveryPolicyEvaluationTotals
    """Counts of conditional preparation decisions and observed application pressure for the sampled candidates."""
    candidates: list[WorkflowRecoveryPolicyEvaluationCandidate]
    coverage_notes: list[str]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        failure_coverage = self.failure_coverage.to_dict()

        current = self.current.to_dict()

        proposed = self.proposed.to_dict()

        observed_at = self.observed_at.isoformat()

        created_after = self.created_after.isoformat()

        created_before = self.created_before.isoformat()

        inspected_reviews = self.inspected_reviews

        reviews_truncated = self.reviews_truncated

        candidates_truncated = self.candidates_truncated

        executions_truncated = self.executions_truncated

        observed_executions = self.observed_executions

        current_totals = self.current_totals.to_dict()

        proposed_totals = self.proposed_totals.to_dict()

        candidates = []
        for candidates_item_data in self.candidates:
            candidates_item = candidates_item_data.to_dict()
            candidates.append(candidates_item)

        coverage_notes = self.coverage_notes

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "failure_coverage": failure_coverage,
                "current": current,
                "proposed": proposed,
                "observed_at": observed_at,
                "created_after": created_after,
                "created_before": created_before,
                "inspected_reviews": inspected_reviews,
                "reviews_truncated": reviews_truncated,
                "candidates_truncated": candidates_truncated,
                "executions_truncated": executions_truncated,
                "observed_executions": observed_executions,
                "current_totals": current_totals,
                "proposed_totals": proposed_totals,
                "candidates": candidates,
                "coverage_notes": coverage_notes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_recovery_failure_coverage import WorkflowRecoveryFailureCoverage
        from ..models.workflow_recovery_policy import WorkflowRecoveryPolicy
        from ..models.workflow_recovery_policy_evaluation_candidate import WorkflowRecoveryPolicyEvaluationCandidate
        from ..models.workflow_recovery_policy_evaluation_totals import WorkflowRecoveryPolicyEvaluationTotals
        from ..models.workflow_recovery_policy_response import WorkflowRecoveryPolicyResponse

        d = dict(src_dict)
        failure_coverage = WorkflowRecoveryFailureCoverage.from_dict(d.pop("failure_coverage"))

        current = WorkflowRecoveryPolicyResponse.from_dict(d.pop("current"))

        proposed = WorkflowRecoveryPolicy.from_dict(d.pop("proposed"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        created_after = datetime.datetime.fromisoformat(d.pop("created_after"))

        created_before = datetime.datetime.fromisoformat(d.pop("created_before"))

        inspected_reviews = d.pop("inspected_reviews")

        reviews_truncated = d.pop("reviews_truncated")

        candidates_truncated = d.pop("candidates_truncated")

        executions_truncated = d.pop("executions_truncated")

        observed_executions = d.pop("observed_executions")

        current_totals = WorkflowRecoveryPolicyEvaluationTotals.from_dict(d.pop("current_totals"))

        proposed_totals = WorkflowRecoveryPolicyEvaluationTotals.from_dict(d.pop("proposed_totals"))

        candidates = []
        _candidates = d.pop("candidates")
        for candidates_item_data in _candidates:
            candidates_item = WorkflowRecoveryPolicyEvaluationCandidate.from_dict(candidates_item_data)

            candidates.append(candidates_item)

        coverage_notes = cast(list[str], d.pop("coverage_notes"))

        workflow_recovery_policy_evaluation = cls(
            failure_coverage=failure_coverage,
            current=current,
            proposed=proposed,
            observed_at=observed_at,
            created_after=created_after,
            created_before=created_before,
            inspected_reviews=inspected_reviews,
            reviews_truncated=reviews_truncated,
            candidates_truncated=candidates_truncated,
            executions_truncated=executions_truncated,
            observed_executions=observed_executions,
            current_totals=current_totals,
            proposed_totals=proposed_totals,
            candidates=candidates,
            coverage_notes=coverage_notes,
        )

        workflow_recovery_policy_evaluation.additional_properties = d
        return workflow_recovery_policy_evaluation

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
