from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_active_condition_summary import OperationWorkflowActiveConditionSummary
    from ..models.operation_workflow_condition_coverage import OperationWorkflowConditionCoverage
    from ..models.operation_workflow_condition_outcome_summary import OperationWorkflowConditionOutcomeSummary
    from ..models.operation_workflow_condition_refresh_progress import OperationWorkflowConditionRefreshProgress
    from ..models.operation_workflow_decision_outcome_summary import OperationWorkflowDecisionOutcomeSummary
    from ..models.operation_workflow_missing_condition_progress import OperationWorkflowMissingConditionProgress
    from ..models.operation_workflow_new_missing_condition_progress import OperationWorkflowNewMissingConditionProgress
    from ..models.operation_workflow_performance_cohort import OperationWorkflowPerformanceCohort


T = TypeVar("T", bound="OperationWorkflowPerformanceSummary")


@_attrs_define
class OperationWorkflowPerformanceSummary:
    """Completed and ongoing workflow cohorts with bounded sampling, history coverage and duration distributions."""

    cohort_token: str
    """Opaque selector-bound token for opening consistent contributor views."""
    evaluated_at: datetime.datetime
    workflow: str
    cohort_limit: int
    completed: OperationWorkflowPerformanceCohort
    """Counts distinguish all matches from the latest retained sample and complete eligible histories. Every
    duration excludes incomplete histories. Groups are ranked by total duration descending with stable identity
    ties. Overall distributions include eligible zero-duration instances; group distributions include only instances
    that reported that group. Overlapping blockers and verification waits are counted separately in their groups.
   """
    ongoing: OperationWorkflowPerformanceCohort
    """Counts distinguish all matches from the latest retained sample and complete eligible histories. Every
    duration excludes incomplete histories. Groups are ranked by total duration descending with stable identity
    ties. Overall distributions include eligible zero-duration instances; group distributions include only instances
    that reported that group. Overlapping blockers and verification waits are counted separately in their groups.
   """
    new_missing_progress: OperationWorkflowNewMissingConditionProgress | Unset = UNSET
    """Optional baseline_token comparison explaining current missing reports relative to retained baseline
    expectations in today's completed and ongoing sample. Current terminal workflows produce no active gaps.
    Publication visibility, retained immutable baseline definitions, continuous baseline report history, conflict
    and evidence limits match missing_progress. Workflows with no retained baseline are not invented as new gaps,
    and unavailable definitions are excluded. This is not original cohort membership or total historical backlog.
    All current missing status counts precede preview caps; missing evidence is not failed verification."""
    missing_progress: OperationWorkflowMissingConditionProgress | Unset = UNSET
    """Optional baseline_token comparison of missing expectations reconstructed from retained reports and the
    baseline reporting Operation's immutable definition in today's completed and ongoing samples. Publication
    timestamps determine baseline visibility. This does not store original cohort membership or prove backlog
    resolution. Missing historical definitions, incomplete report history, conflicts and evidence limits are
    explicitly excluded. Baseline histories must be continuous within one contract; current contract boundaries
    permit contract_changed classification. Prior terminal reports do not create baseline gaps. Counts precede
    change preview limits. Conditions leaving the current state's expectation set, changed declarations and terminal
    workflows do not imply successful verification."""
    condition_coverage: OperationWorkflowConditionCoverage | Unset = UNSET
    """Reporting coverage for current states in the bounded ongoing sample. Expectations come only from the latest
    reporting Operation's immutable definition, not a global registry or every Operation in the workflow. Missing
    means no matching evidence in latest explicit contexts, not a failed check or proof it was never reported
    historically. Full continuous retained history is required. Conflicting results and over-limit evidence are
    excluded. Reported includes overdue, so expected equals reported plus missing and overdue is a subset of
    reported. Counts precede the per-workflow preview cap. No expectations is unconfigured coverage, not
    completeness."""
    refresh_progress: OperationWorkflowConditionRefreshProgress | Unset = UNSET
    """Report freshness progress reconstructed at baseline_token evaluation time using publication visibility in
    current retained completed and ongoing cohorts. This is not a stored baseline cohort, a total historical
    backlog, or business verification. Only continuous retained report history qualifies; contract boundaries are
    permitted for explicit contract_changed classification without relaxing existing duration or active-summary
    eligibility. Unknown history and conflicting or over-limit evidence are excluded. Status totals include all
    baseline overdue identities before per-workflow preview limits. Completed workflows remain visible as terminal
    reports without implying cleared verification."""
    active_conditions: OperationWorkflowActiveConditionSummary | Unset = UNSET
    """Latest explicit condition snapshots in the ongoing sample with complete retained history. The latest reason
    and latest nonempty selected-path snapshot provide independent contexts; a path without a fact clears that path
    context. Metadata-only reports preserve evidence. Explicit decision omission removes older conditions. Contract
    changes reset contexts. Conflicting results for an exact identity exclude the workflow. Missing conditions
    remain unreported. At most 256 contexts and 256 final condition identities per workflow qualify. Groups overlap;
    no present business truth or causation is inferred."""
    condition_outcomes: OperationWorkflowConditionOutcomeSummary | Unset = UNSET
    """Observed condition/result associations with latest reported outcomes in the completed performance sample.
    Only complete histories with a reported outcome and at most 256 exact condition identities qualify. Reasons and
    selected-path decision facts at or before the first terminal report supply explicit results. Missing conditions
    are unreported; unknown is explicit. Groups overlap and do not establish causation."""
    decision_outcomes: OperationWorkflowDecisionOutcomeSummary | Unset = UNSET
    """Observed decision and path associations with latest reported outcomes in the completed performance cohort.
    Only complete histories with a reported outcome qualify. Repeated decision or path identities count once per
    workflow and group. Groups overlap; their counts must not be summed. Classification uses reasons and selections
    reported at or before the first terminal report. Sequence does not establish causation. Groups retain the first
    64 canonical identities in ascending order."""

    def to_dict(self) -> dict[str, Any]:
        cohort_token = self.cohort_token

        evaluated_at = self.evaluated_at.isoformat()

        workflow = self.workflow

        cohort_limit = self.cohort_limit

        completed = self.completed.to_dict()

        ongoing = self.ongoing.to_dict()

        new_missing_progress: dict[str, Any] | Unset = UNSET
        if not isinstance(self.new_missing_progress, Unset):
            new_missing_progress = self.new_missing_progress.to_dict()

        missing_progress: dict[str, Any] | Unset = UNSET
        if not isinstance(self.missing_progress, Unset):
            missing_progress = self.missing_progress.to_dict()

        condition_coverage: dict[str, Any] | Unset = UNSET
        if not isinstance(self.condition_coverage, Unset):
            condition_coverage = self.condition_coverage.to_dict()

        refresh_progress: dict[str, Any] | Unset = UNSET
        if not isinstance(self.refresh_progress, Unset):
            refresh_progress = self.refresh_progress.to_dict()

        active_conditions: dict[str, Any] | Unset = UNSET
        if not isinstance(self.active_conditions, Unset):
            active_conditions = self.active_conditions.to_dict()

        condition_outcomes: dict[str, Any] | Unset = UNSET
        if not isinstance(self.condition_outcomes, Unset):
            condition_outcomes = self.condition_outcomes.to_dict()

        decision_outcomes: dict[str, Any] | Unset = UNSET
        if not isinstance(self.decision_outcomes, Unset):
            decision_outcomes = self.decision_outcomes.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "cohort_token": cohort_token,
                "evaluated_at": evaluated_at,
                "workflow": workflow,
                "cohort_limit": cohort_limit,
                "completed": completed,
                "ongoing": ongoing,
            }
        )
        if new_missing_progress is not UNSET:
            field_dict["new_missing_progress"] = new_missing_progress
        if missing_progress is not UNSET:
            field_dict["missing_progress"] = missing_progress
        if condition_coverage is not UNSET:
            field_dict["condition_coverage"] = condition_coverage
        if refresh_progress is not UNSET:
            field_dict["refresh_progress"] = refresh_progress
        if active_conditions is not UNSET:
            field_dict["active_conditions"] = active_conditions
        if condition_outcomes is not UNSET:
            field_dict["condition_outcomes"] = condition_outcomes
        if decision_outcomes is not UNSET:
            field_dict["decision_outcomes"] = decision_outcomes

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_active_condition_summary import OperationWorkflowActiveConditionSummary
        from ..models.operation_workflow_condition_coverage import OperationWorkflowConditionCoverage
        from ..models.operation_workflow_condition_outcome_summary import OperationWorkflowConditionOutcomeSummary
        from ..models.operation_workflow_condition_refresh_progress import OperationWorkflowConditionRefreshProgress
        from ..models.operation_workflow_decision_outcome_summary import OperationWorkflowDecisionOutcomeSummary
        from ..models.operation_workflow_missing_condition_progress import OperationWorkflowMissingConditionProgress
        from ..models.operation_workflow_new_missing_condition_progress import (
            OperationWorkflowNewMissingConditionProgress,
        )
        from ..models.operation_workflow_performance_cohort import OperationWorkflowPerformanceCohort

        d = dict(src_dict)
        cohort_token = d.pop("cohort_token")

        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        workflow = d.pop("workflow")

        cohort_limit = d.pop("cohort_limit")

        completed = OperationWorkflowPerformanceCohort.from_dict(d.pop("completed"))

        ongoing = OperationWorkflowPerformanceCohort.from_dict(d.pop("ongoing"))

        _new_missing_progress = d.pop("new_missing_progress", UNSET)
        new_missing_progress: OperationWorkflowNewMissingConditionProgress | Unset
        if isinstance(_new_missing_progress, Unset):
            new_missing_progress = UNSET
        else:
            new_missing_progress = OperationWorkflowNewMissingConditionProgress.from_dict(_new_missing_progress)

        _missing_progress = d.pop("missing_progress", UNSET)
        missing_progress: OperationWorkflowMissingConditionProgress | Unset
        if isinstance(_missing_progress, Unset):
            missing_progress = UNSET
        else:
            missing_progress = OperationWorkflowMissingConditionProgress.from_dict(_missing_progress)

        _condition_coverage = d.pop("condition_coverage", UNSET)
        condition_coverage: OperationWorkflowConditionCoverage | Unset
        if isinstance(_condition_coverage, Unset):
            condition_coverage = UNSET
        else:
            condition_coverage = OperationWorkflowConditionCoverage.from_dict(_condition_coverage)

        _refresh_progress = d.pop("refresh_progress", UNSET)
        refresh_progress: OperationWorkflowConditionRefreshProgress | Unset
        if isinstance(_refresh_progress, Unset):
            refresh_progress = UNSET
        else:
            refresh_progress = OperationWorkflowConditionRefreshProgress.from_dict(_refresh_progress)

        _active_conditions = d.pop("active_conditions", UNSET)
        active_conditions: OperationWorkflowActiveConditionSummary | Unset
        if isinstance(_active_conditions, Unset):
            active_conditions = UNSET
        else:
            active_conditions = OperationWorkflowActiveConditionSummary.from_dict(_active_conditions)

        _condition_outcomes = d.pop("condition_outcomes", UNSET)
        condition_outcomes: OperationWorkflowConditionOutcomeSummary | Unset
        if isinstance(_condition_outcomes, Unset):
            condition_outcomes = UNSET
        else:
            condition_outcomes = OperationWorkflowConditionOutcomeSummary.from_dict(_condition_outcomes)

        _decision_outcomes = d.pop("decision_outcomes", UNSET)
        decision_outcomes: OperationWorkflowDecisionOutcomeSummary | Unset
        if isinstance(_decision_outcomes, Unset):
            decision_outcomes = UNSET
        else:
            decision_outcomes = OperationWorkflowDecisionOutcomeSummary.from_dict(_decision_outcomes)

        operation_workflow_performance_summary = cls(
            cohort_token=cohort_token,
            evaluated_at=evaluated_at,
            workflow=workflow,
            cohort_limit=cohort_limit,
            completed=completed,
            ongoing=ongoing,
            new_missing_progress=new_missing_progress,
            missing_progress=missing_progress,
            condition_coverage=condition_coverage,
            refresh_progress=refresh_progress,
            active_conditions=active_conditions,
            condition_outcomes=condition_outcomes,
            decision_outcomes=decision_outcomes,
        )

        return operation_workflow_performance_summary
