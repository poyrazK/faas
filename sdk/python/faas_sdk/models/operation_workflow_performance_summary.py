from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_condition_outcome_summary import OperationWorkflowConditionOutcomeSummary
    from ..models.operation_workflow_decision_outcome_summary import OperationWorkflowDecisionOutcomeSummary
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
        if condition_outcomes is not UNSET:
            field_dict["condition_outcomes"] = condition_outcomes
        if decision_outcomes is not UNSET:
            field_dict["decision_outcomes"] = decision_outcomes

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_condition_outcome_summary import OperationWorkflowConditionOutcomeSummary
        from ..models.operation_workflow_decision_outcome_summary import OperationWorkflowDecisionOutcomeSummary
        from ..models.operation_workflow_performance_cohort import OperationWorkflowPerformanceCohort

        d = dict(src_dict)
        cohort_token = d.pop("cohort_token")

        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        workflow = d.pop("workflow")

        cohort_limit = d.pop("cohort_limit")

        completed = OperationWorkflowPerformanceCohort.from_dict(d.pop("completed"))

        ongoing = OperationWorkflowPerformanceCohort.from_dict(d.pop("ongoing"))

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
            condition_outcomes=condition_outcomes,
            decision_outcomes=decision_outcomes,
        )

        return operation_workflow_performance_summary
