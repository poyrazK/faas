from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_duration_distribution import OperationWorkflowDurationDistribution
    from ..models.operation_workflow_rule_version_outcome import OperationWorkflowRuleVersionOutcome


T = TypeVar("T", bound="OperationWorkflowRuleVersionCohort")


@_attrs_define
class OperationWorkflowRuleVersionCohort:
    """Complete completed workflows with a latest reported outcome and exactly one observed version for the same
    contract/decision/rule identity before the first terminal report. Detection occurs before version truncation.
    Incomplete histories never qualify. Statistics exclude overlapping workflows and are still observational rather than
    causal.

    """

    eligible_workflow_count: int
    completion_time: OperationWorkflowDurationDistribution
    """One accumulated duration per eligible workflow. Nearest-rank percentiles are zero when workflow_count is
    zero."""
    blocked_time: OperationWorkflowDurationDistribution
    """One accumulated duration per eligible workflow. Nearest-rank percentiles are zero when workflow_count is
    zero."""
    outcomes_truncated: bool
    outcomes: list[OperationWorkflowRuleVersionOutcome]

    def to_dict(self) -> dict[str, Any]:
        eligible_workflow_count = self.eligible_workflow_count

        completion_time = self.completion_time.to_dict()

        blocked_time = self.blocked_time.to_dict()

        outcomes_truncated = self.outcomes_truncated

        outcomes = []
        for outcomes_item_data in self.outcomes:
            outcomes_item = outcomes_item_data.to_dict()
            outcomes.append(outcomes_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "eligible_workflow_count": eligible_workflow_count,
                "completion_time": completion_time,
                "blocked_time": blocked_time,
                "outcomes_truncated": outcomes_truncated,
                "outcomes": outcomes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_duration_distribution import OperationWorkflowDurationDistribution
        from ..models.operation_workflow_rule_version_outcome import OperationWorkflowRuleVersionOutcome

        d = dict(src_dict)
        eligible_workflow_count = d.pop("eligible_workflow_count")

        completion_time = OperationWorkflowDurationDistribution.from_dict(d.pop("completion_time"))

        blocked_time = OperationWorkflowDurationDistribution.from_dict(d.pop("blocked_time"))

        outcomes_truncated = d.pop("outcomes_truncated")

        outcomes = []
        _outcomes = d.pop("outcomes")
        for outcomes_item_data in _outcomes:
            outcomes_item = OperationWorkflowRuleVersionOutcome.from_dict(outcomes_item_data)

            outcomes.append(outcomes_item)

        operation_workflow_rule_version_cohort = cls(
            eligible_workflow_count=eligible_workflow_count,
            completion_time=completion_time,
            blocked_time=blocked_time,
            outcomes_truncated=outcomes_truncated,
            outcomes=outcomes,
        )

        return operation_workflow_rule_version_cohort
