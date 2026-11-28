from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_blocker_performance import OperationWorkflowBlockerPerformance
    from ..models.operation_workflow_duration_distribution import OperationWorkflowDurationDistribution
    from ..models.operation_workflow_performance_coverage_reason import OperationWorkflowPerformanceCoverageReason
    from ..models.operation_workflow_state_performance import OperationWorkflowStatePerformance
    from ..models.operation_workflow_verification_performance import OperationWorkflowVerificationPerformance


T = TypeVar("T", bound="OperationWorkflowPerformanceCohort")


@_attrs_define
class OperationWorkflowPerformanceCohort:
    """Counts distinguish all matches from the latest retained sample and complete eligible histories. Every duration
    excludes incomplete histories. Groups are ranked by total duration descending with stable identity ties. Overall
    distributions include eligible zero-duration instances; group distributions include only instances that reported
    that group. Overlapping blockers and verification waits are counted separately in their groups.

    """

    sla_configured_workflow_count: int
    sla_evaluated_workflow_count: int
    sla_breached_workflow_count: int
    sla_unknown_workflow_count: int
    matching_workflow_count: int
    sampled_workflow_count: int
    complete_history_workflow_count: int
    excluded_incomplete_workflow_count: int
    cohort_truncated: bool
    exclusions: list[OperationWorkflowPerformanceCoverageReason]
    state_time: OperationWorkflowDurationDistribution
    """One accumulated duration per eligible workflow. Nearest-rank percentiles are zero when workflow_count is
    zero."""
    blocked_time: OperationWorkflowDurationDistribution
    """One accumulated duration per eligible workflow. Nearest-rank percentiles are zero when workflow_count is
    zero."""
    verification_wait: OperationWorkflowDurationDistribution
    """One accumulated duration per eligible workflow. Nearest-rank percentiles are zero when workflow_count is
    zero."""
    states: list[OperationWorkflowStatePerformance]
    blockers: list[OperationWorkflowBlockerPerformance]
    verification_owners: list[OperationWorkflowVerificationPerformance]
    states_truncated: bool
    blockers_truncated: bool
    verification_owners_truncated: bool

    def to_dict(self) -> dict[str, Any]:
        sla_configured_workflow_count = self.sla_configured_workflow_count

        sla_evaluated_workflow_count = self.sla_evaluated_workflow_count

        sla_breached_workflow_count = self.sla_breached_workflow_count

        sla_unknown_workflow_count = self.sla_unknown_workflow_count

        matching_workflow_count = self.matching_workflow_count

        sampled_workflow_count = self.sampled_workflow_count

        complete_history_workflow_count = self.complete_history_workflow_count

        excluded_incomplete_workflow_count = self.excluded_incomplete_workflow_count

        cohort_truncated = self.cohort_truncated

        exclusions = []
        for exclusions_item_data in self.exclusions:
            exclusions_item = exclusions_item_data.to_dict()
            exclusions.append(exclusions_item)

        state_time = self.state_time.to_dict()

        blocked_time = self.blocked_time.to_dict()

        verification_wait = self.verification_wait.to_dict()

        states = []
        for states_item_data in self.states:
            states_item = states_item_data.to_dict()
            states.append(states_item)

        blockers = []
        for blockers_item_data in self.blockers:
            blockers_item = blockers_item_data.to_dict()
            blockers.append(blockers_item)

        verification_owners = []
        for verification_owners_item_data in self.verification_owners:
            verification_owners_item = verification_owners_item_data.to_dict()
            verification_owners.append(verification_owners_item)

        states_truncated = self.states_truncated

        blockers_truncated = self.blockers_truncated

        verification_owners_truncated = self.verification_owners_truncated

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "sla_configured_workflow_count": sla_configured_workflow_count,
                "sla_evaluated_workflow_count": sla_evaluated_workflow_count,
                "sla_breached_workflow_count": sla_breached_workflow_count,
                "sla_unknown_workflow_count": sla_unknown_workflow_count,
                "matching_workflow_count": matching_workflow_count,
                "sampled_workflow_count": sampled_workflow_count,
                "complete_history_workflow_count": complete_history_workflow_count,
                "excluded_incomplete_workflow_count": excluded_incomplete_workflow_count,
                "cohort_truncated": cohort_truncated,
                "exclusions": exclusions,
                "state_time": state_time,
                "blocked_time": blocked_time,
                "verification_wait": verification_wait,
                "states": states,
                "blockers": blockers,
                "verification_owners": verification_owners,
                "states_truncated": states_truncated,
                "blockers_truncated": blockers_truncated,
                "verification_owners_truncated": verification_owners_truncated,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_blocker_performance import OperationWorkflowBlockerPerformance
        from ..models.operation_workflow_duration_distribution import OperationWorkflowDurationDistribution
        from ..models.operation_workflow_performance_coverage_reason import OperationWorkflowPerformanceCoverageReason
        from ..models.operation_workflow_state_performance import OperationWorkflowStatePerformance
        from ..models.operation_workflow_verification_performance import OperationWorkflowVerificationPerformance

        d = dict(src_dict)
        sla_configured_workflow_count = d.pop("sla_configured_workflow_count")

        sla_evaluated_workflow_count = d.pop("sla_evaluated_workflow_count")

        sla_breached_workflow_count = d.pop("sla_breached_workflow_count")

        sla_unknown_workflow_count = d.pop("sla_unknown_workflow_count")

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

        state_time = OperationWorkflowDurationDistribution.from_dict(d.pop("state_time"))

        blocked_time = OperationWorkflowDurationDistribution.from_dict(d.pop("blocked_time"))

        verification_wait = OperationWorkflowDurationDistribution.from_dict(d.pop("verification_wait"))

        states = []
        _states = d.pop("states")
        for states_item_data in _states:
            states_item = OperationWorkflowStatePerformance.from_dict(states_item_data)

            states.append(states_item)

        blockers = []
        _blockers = d.pop("blockers")
        for blockers_item_data in _blockers:
            blockers_item = OperationWorkflowBlockerPerformance.from_dict(blockers_item_data)

            blockers.append(blockers_item)

        verification_owners = []
        _verification_owners = d.pop("verification_owners")
        for verification_owners_item_data in _verification_owners:
            verification_owners_item = OperationWorkflowVerificationPerformance.from_dict(verification_owners_item_data)

            verification_owners.append(verification_owners_item)

        states_truncated = d.pop("states_truncated")

        blockers_truncated = d.pop("blockers_truncated")

        verification_owners_truncated = d.pop("verification_owners_truncated")

        operation_workflow_performance_cohort = cls(
            sla_configured_workflow_count=sla_configured_workflow_count,
            sla_evaluated_workflow_count=sla_evaluated_workflow_count,
            sla_breached_workflow_count=sla_breached_workflow_count,
            sla_unknown_workflow_count=sla_unknown_workflow_count,
            matching_workflow_count=matching_workflow_count,
            sampled_workflow_count=sampled_workflow_count,
            complete_history_workflow_count=complete_history_workflow_count,
            excluded_incomplete_workflow_count=excluded_incomplete_workflow_count,
            cohort_truncated=cohort_truncated,
            exclusions=exclusions,
            state_time=state_time,
            blocked_time=blocked_time,
            verification_wait=verification_wait,
            states=states,
            blockers=blockers,
            verification_owners=verification_owners,
            states_truncated=states_truncated,
            blockers_truncated=blockers_truncated,
            verification_owners_truncated=verification_owners_truncated,
        )

        return operation_workflow_performance_cohort
