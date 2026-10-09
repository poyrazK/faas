from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_performance_cohort import OperationWorkflowPerformanceCohort


T = TypeVar("T", bound="OperationWorkflowPerformanceSummary")


@_attrs_define
class OperationWorkflowPerformanceSummary:
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

    def to_dict(self) -> dict[str, Any]:
        cohort_token = self.cohort_token

        evaluated_at = self.evaluated_at.isoformat()

        workflow = self.workflow

        cohort_limit = self.cohort_limit

        completed = self.completed.to_dict()

        ongoing = self.ongoing.to_dict()

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

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_performance_cohort import OperationWorkflowPerformanceCohort

        d = dict(src_dict)
        cohort_token = d.pop("cohort_token")

        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        workflow = d.pop("workflow")

        cohort_limit = d.pop("cohort_limit")

        completed = OperationWorkflowPerformanceCohort.from_dict(d.pop("completed"))

        ongoing = OperationWorkflowPerformanceCohort.from_dict(d.pop("ongoing"))

        operation_workflow_performance_summary = cls(
            cohort_token=cohort_token,
            evaluated_at=evaluated_at,
            workflow=workflow,
            cohort_limit=cohort_limit,
            completed=completed,
            ongoing=ongoing,
        )

        return operation_workflow_performance_summary
