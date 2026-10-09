from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_performance_instances_response_cohort import (
    OperationWorkflowPerformanceInstancesResponseCohort,
    check_operation_workflow_performance_instances_response_cohort,
)

if TYPE_CHECKING:
    from ..models.operation_workflow_duration_distribution import OperationWorkflowDurationDistribution
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

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_duration_distribution import OperationWorkflowDurationDistribution
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
        )

        return operation_workflow_performance_instances_response
