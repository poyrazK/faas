from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_bottlenecks_incomplete_reasons_item import (
    OperationWorkflowBottlenecksIncompleteReasonsItem,
    check_operation_workflow_bottlenecks_incomplete_reasons_item,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_blocker_duration import OperationWorkflowBlockerDuration
    from ..models.operation_workflow_state_duration import OperationWorkflowStateDuration
    from ..models.operation_workflow_verification_duration import OperationWorkflowVerificationDuration


T = TypeVar("T", bound="OperationWorkflowBottlenecks")


@_attrs_define
class OperationWorkflowBottlenecks:
    """Observed durations for one retained workflow instance. State/blocker intervals use application occurrence time.
    Verification waits use platform publication time. Gaps are excluded and incomplete histories are marked. Groups are
    sorted by observed duration and bounded independently of exact window totals.

    """

    evaluated_at: datetime.datetime
    history_complete: bool
    """True only when the retained window starts at revision 1 and reaches the current report without gaps or
    invalid interval boundaries."""
    incomplete_reasons: list[OperationWorkflowBottlenecksIncompleteReasonsItem]
    reports_in_window: int
    history_truncated: bool
    ongoing: bool
    """Current workflow state is nonterminal. Ongoing duration is evaluated as of evaluated_at."""
    state_seconds: int
    blocked_seconds: int
    """Union of measured intervals with at least one active blocker. Overlapping blocker groups can sum to more
    than this total."""
    verification_unknown_start_count: int
    verification_wait_seconds: int
    """Sum of whole observed wait seconds across obligations with known publication starts in the history window.
    Overlapping waits are counted separately."""
    states: list[OperationWorkflowStateDuration]
    blockers: list[OperationWorkflowBlockerDuration]
    verification_owners: list[OperationWorkflowVerificationDuration]
    states_truncated: bool
    blockers_truncated: bool
    verification_owners_truncated: bool
    observed_from: datetime.datetime | Unset = UNSET
    observed_through: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        evaluated_at = self.evaluated_at.isoformat()

        history_complete = self.history_complete

        incomplete_reasons = []
        for incomplete_reasons_item_data in self.incomplete_reasons:
            incomplete_reasons_item: str = incomplete_reasons_item_data
            incomplete_reasons.append(incomplete_reasons_item)

        reports_in_window = self.reports_in_window

        history_truncated = self.history_truncated

        ongoing = self.ongoing

        state_seconds = self.state_seconds

        blocked_seconds = self.blocked_seconds

        verification_unknown_start_count = self.verification_unknown_start_count

        verification_wait_seconds = self.verification_wait_seconds

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

        observed_from: str | Unset = UNSET
        if not isinstance(self.observed_from, Unset):
            observed_from = self.observed_from.isoformat()

        observed_through: str | Unset = UNSET
        if not isinstance(self.observed_through, Unset):
            observed_through = self.observed_through.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "evaluated_at": evaluated_at,
                "history_complete": history_complete,
                "incomplete_reasons": incomplete_reasons,
                "reports_in_window": reports_in_window,
                "history_truncated": history_truncated,
                "ongoing": ongoing,
                "state_seconds": state_seconds,
                "blocked_seconds": blocked_seconds,
                "verification_unknown_start_count": verification_unknown_start_count,
                "verification_wait_seconds": verification_wait_seconds,
                "states": states,
                "blockers": blockers,
                "verification_owners": verification_owners,
                "states_truncated": states_truncated,
                "blockers_truncated": blockers_truncated,
                "verification_owners_truncated": verification_owners_truncated,
            }
        )
        if observed_from is not UNSET:
            field_dict["observed_from"] = observed_from
        if observed_through is not UNSET:
            field_dict["observed_through"] = observed_through

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_blocker_duration import OperationWorkflowBlockerDuration
        from ..models.operation_workflow_state_duration import OperationWorkflowStateDuration
        from ..models.operation_workflow_verification_duration import OperationWorkflowVerificationDuration

        d = dict(src_dict)
        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        history_complete = d.pop("history_complete")

        incomplete_reasons = []
        _incomplete_reasons = d.pop("incomplete_reasons")
        for incomplete_reasons_item_data in _incomplete_reasons:
            incomplete_reasons_item = check_operation_workflow_bottlenecks_incomplete_reasons_item(
                incomplete_reasons_item_data
            )

            incomplete_reasons.append(incomplete_reasons_item)

        reports_in_window = d.pop("reports_in_window")

        history_truncated = d.pop("history_truncated")

        ongoing = d.pop("ongoing")

        state_seconds = d.pop("state_seconds")

        blocked_seconds = d.pop("blocked_seconds")

        verification_unknown_start_count = d.pop("verification_unknown_start_count")

        verification_wait_seconds = d.pop("verification_wait_seconds")

        states = []
        _states = d.pop("states")
        for states_item_data in _states:
            states_item = OperationWorkflowStateDuration.from_dict(states_item_data)

            states.append(states_item)

        blockers = []
        _blockers = d.pop("blockers")
        for blockers_item_data in _blockers:
            blockers_item = OperationWorkflowBlockerDuration.from_dict(blockers_item_data)

            blockers.append(blockers_item)

        verification_owners = []
        _verification_owners = d.pop("verification_owners")
        for verification_owners_item_data in _verification_owners:
            verification_owners_item = OperationWorkflowVerificationDuration.from_dict(verification_owners_item_data)

            verification_owners.append(verification_owners_item)

        states_truncated = d.pop("states_truncated")

        blockers_truncated = d.pop("blockers_truncated")

        verification_owners_truncated = d.pop("verification_owners_truncated")

        _observed_from = d.pop("observed_from", UNSET)
        observed_from: datetime.datetime | Unset
        if isinstance(_observed_from, Unset):
            observed_from = UNSET
        else:
            observed_from = datetime.datetime.fromisoformat(_observed_from)

        _observed_through = d.pop("observed_through", UNSET)
        observed_through: datetime.datetime | Unset
        if isinstance(_observed_through, Unset):
            observed_through = UNSET
        else:
            observed_through = datetime.datetime.fromisoformat(_observed_through)

        operation_workflow_bottlenecks = cls(
            evaluated_at=evaluated_at,
            history_complete=history_complete,
            incomplete_reasons=incomplete_reasons,
            reports_in_window=reports_in_window,
            history_truncated=history_truncated,
            ongoing=ongoing,
            state_seconds=state_seconds,
            blocked_seconds=blocked_seconds,
            verification_unknown_start_count=verification_unknown_start_count,
            verification_wait_seconds=verification_wait_seconds,
            states=states,
            blockers=blockers,
            verification_owners=verification_owners,
            states_truncated=states_truncated,
            blockers_truncated=blockers_truncated,
            verification_owners_truncated=verification_owners_truncated,
            observed_from=observed_from,
            observed_through=observed_through,
        )

        return operation_workflow_bottlenecks
