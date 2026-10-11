from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_performance_instances_response_cohort import (
    OperationWorkflowPerformanceInstancesResponseCohort,
    check_operation_workflow_performance_instances_response_cohort,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_active_condition_summary import OperationWorkflowActiveConditionSummary
    from ..models.operation_workflow_condition_coverage import OperationWorkflowConditionCoverage
    from ..models.operation_workflow_condition_outcome_summary import OperationWorkflowConditionOutcomeSummary
    from ..models.operation_workflow_decision_outcome_summary import OperationWorkflowDecisionOutcomeSummary
    from ..models.operation_workflow_duration_distribution import OperationWorkflowDurationDistribution
    from ..models.operation_workflow_new_missing_condition_progress import OperationWorkflowNewMissingConditionProgress
    from ..models.operation_workflow_performance_coverage_reason import OperationWorkflowPerformanceCoverageReason
    from ..models.operation_workflow_performance_group import OperationWorkflowPerformanceGroup
    from ..models.operation_workflow_performance_instance import OperationWorkflowPerformanceInstance


T = TypeVar("T", bound="OperationWorkflowPerformanceInstancesResponse")


@_attrs_define
class OperationWorkflowPerformanceInstancesResponse:
    """Coverage counts cover the selected cohort before dimension selection. Duration statistics and ranked items include
    only complete-history instances contributing to the exact dimension. Overall dimensions include eligible zero
    values. The token binds both sampled cohorts and evidence at evaluated_at; it does not store a database snapshot.

    """

    evaluated_at: datetime.datetime
    cohort_token: str
    workflow: str
    cohort: OperationWorkflowPerformanceInstancesResponseCohort
    group: OperationWorkflowPerformanceGroup
    """Exact duration dimension and optional contract, state, blocker or owner selectors for contributor reads."""
    matching_workflow_count: int
    sampled_workflow_count: int
    complete_history_workflow_count: int
    excluded_incomplete_workflow_count: int
    cohort_truncated: bool
    exclusions: list[OperationWorkflowPerformanceCoverageReason]
    duration: OperationWorkflowDurationDistribution
    """One accumulated duration per eligible workflow. Nearest-rank percentiles are zero when workflow_count is
    zero."""
    items: list[OperationWorkflowPerformanceInstance]
    new_missing_progress: OperationWorkflowNewMissingConditionProgress | Unset = UNSET
    """Optional baseline_token comparison explaining current missing reports relative to retained baseline
    expectations in today's completed and ongoing sample. Current terminal workflows produce no active gaps.
    Publication visibility, retained immutable baseline definitions, continuous baseline report history, conflict
    and evidence limits match missing_progress. Workflows with no retained baseline are not invented as new gaps,
    and unavailable definitions are excluded. This is not original cohort membership or total historical backlog.
    All current missing status counts precede preview caps; missing evidence is not failed verification."""
    condition_coverage: OperationWorkflowConditionCoverage | Unset = UNSET
    """Reporting coverage for current states in the bounded ongoing sample. Expectations come only from the latest
    reporting Operation's immutable definition, not a global registry or every Operation in the workflow. Missing
    means no matching evidence in latest explicit contexts, not a failed check or proof it was never reported
    historically. Full continuous retained history is required. Conflicting results and over-limit evidence are
    excluded. Reported includes overdue, so expected equals reported plus missing and overdue is a subset of
    reported. Counts precede the per-workflow preview cap. No expectations is unconfigured coverage, not
    completeness."""
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
        evaluated_at = self.evaluated_at.isoformat()

        cohort_token = self.cohort_token

        workflow = self.workflow

        cohort: str = self.cohort

        group = self.group.to_dict()

        matching_workflow_count = self.matching_workflow_count

        sampled_workflow_count = self.sampled_workflow_count

        complete_history_workflow_count = self.complete_history_workflow_count

        excluded_incomplete_workflow_count = self.excluded_incomplete_workflow_count

        cohort_truncated = self.cohort_truncated

        exclusions = []
        for exclusions_item_data in self.exclusions:
            exclusions_item = exclusions_item_data.to_dict()
            exclusions.append(exclusions_item)

        duration = self.duration.to_dict()

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        new_missing_progress: dict[str, Any] | Unset = UNSET
        if not isinstance(self.new_missing_progress, Unset):
            new_missing_progress = self.new_missing_progress.to_dict()

        condition_coverage: dict[str, Any] | Unset = UNSET
        if not isinstance(self.condition_coverage, Unset):
            condition_coverage = self.condition_coverage.to_dict()

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
                "evaluated_at": evaluated_at,
                "cohort_token": cohort_token,
                "workflow": workflow,
                "cohort": cohort,
                "group": group,
                "matching_workflow_count": matching_workflow_count,
                "sampled_workflow_count": sampled_workflow_count,
                "complete_history_workflow_count": complete_history_workflow_count,
                "excluded_incomplete_workflow_count": excluded_incomplete_workflow_count,
                "cohort_truncated": cohort_truncated,
                "exclusions": exclusions,
                "duration": duration,
                "items": items,
            }
        )
        if new_missing_progress is not UNSET:
            field_dict["new_missing_progress"] = new_missing_progress
        if condition_coverage is not UNSET:
            field_dict["condition_coverage"] = condition_coverage
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
        from ..models.operation_workflow_decision_outcome_summary import OperationWorkflowDecisionOutcomeSummary
        from ..models.operation_workflow_duration_distribution import OperationWorkflowDurationDistribution
        from ..models.operation_workflow_new_missing_condition_progress import (
            OperationWorkflowNewMissingConditionProgress,
        )
        from ..models.operation_workflow_performance_coverage_reason import OperationWorkflowPerformanceCoverageReason
        from ..models.operation_workflow_performance_group import OperationWorkflowPerformanceGroup
        from ..models.operation_workflow_performance_instance import OperationWorkflowPerformanceInstance

        d = dict(src_dict)
        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        cohort_token = d.pop("cohort_token")

        workflow = d.pop("workflow")

        cohort = check_operation_workflow_performance_instances_response_cohort(d.pop("cohort"))

        group = OperationWorkflowPerformanceGroup.from_dict(d.pop("group"))

        matching_workflow_count = d.pop("matching_workflow_count")

        sampled_workflow_count = d.pop("sampled_workflow_count")

        complete_history_workflow_count = d.pop("complete_history_workflow_count")

        excluded_incomplete_workflow_count = d.pop("excluded_incomplete_workflow_count")

        cohort_truncated = d.pop("cohort_truncated")

        exclusions = []
        _exclusions = d.pop("exclusions")
        for exclusions_item_data in _exclusions:
            exclusions_item = OperationWorkflowPerformanceCoverageReason.from_dict(exclusions_item_data)

            exclusions.append(exclusions_item)

        duration = OperationWorkflowDurationDistribution.from_dict(d.pop("duration"))

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = OperationWorkflowPerformanceInstance.from_dict(items_item_data)

            items.append(items_item)

        _new_missing_progress = d.pop("new_missing_progress", UNSET)
        new_missing_progress: OperationWorkflowNewMissingConditionProgress | Unset
        if isinstance(_new_missing_progress, Unset):
            new_missing_progress = UNSET
        else:
            new_missing_progress = OperationWorkflowNewMissingConditionProgress.from_dict(_new_missing_progress)

        _condition_coverage = d.pop("condition_coverage", UNSET)
        condition_coverage: OperationWorkflowConditionCoverage | Unset
        if isinstance(_condition_coverage, Unset):
            condition_coverage = UNSET
        else:
            condition_coverage = OperationWorkflowConditionCoverage.from_dict(_condition_coverage)

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

        operation_workflow_performance_instances_response = cls(
            evaluated_at=evaluated_at,
            cohort_token=cohort_token,
            workflow=workflow,
            cohort=cohort,
            group=group,
            matching_workflow_count=matching_workflow_count,
            sampled_workflow_count=sampled_workflow_count,
            complete_history_workflow_count=complete_history_workflow_count,
            excluded_incomplete_workflow_count=excluded_incomplete_workflow_count,
            cohort_truncated=cohort_truncated,
            exclusions=exclusions,
            duration=duration,
            items=items,
            new_missing_progress=new_missing_progress,
            condition_coverage=condition_coverage,
            active_conditions=active_conditions,
            condition_outcomes=condition_outcomes,
            decision_outcomes=decision_outcomes,
        )

        return operation_workflow_performance_instances_response
