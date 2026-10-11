from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.automation_failure_policy import AutomationFailurePolicy
    from ..models.automation_failure_transition import AutomationFailureTransition


T = TypeVar("T", bound="AutomationFailurePolicyResponse")


@_attrs_define
class AutomationFailurePolicyResponse:
    """Failure guard state, monitoring configuration and advisory retained-work preview."""

    policy: AutomationFailurePolicy
    """Opt-in thresholds for scheduler-enforced failure admission pausing."""
    paused: bool
    """Whether automatic admissions are blocked by the durable failure guard."""
    generation: int
    """Current guard generation; resume requires this value."""
    observed_failures: int
    """Failed and dead runs completed in the current bounded observation window."""
    observed_completed_runs: int
    """Succeeded, failed and dead runs completed in the current observation window."""
    pending_runs: int
    """Existing pending runs that continue despite a failure admission pause."""
    running_runs: int
    """Already running workflows unaffected by admission pausing."""
    waiting_runs: int
    """Existing workflows parked on timers or external events."""
    retained_events: int
    """Advisory count of unadmitted retained event recipients for this automation."""
    history: list[AutomationFailureTransition]
    """Latest pause and resume transitions in descending generation order."""
    monitoring_since: datetime.datetime | Unset = UNSET
    """Start of the fresh monitoring epoch; absent before configuration."""
    paused_at: datetime.datetime | Unset = UNSET
    """Time the current failure pause was latched; absent while unpaused."""

    def to_dict(self) -> dict[str, Any]:
        policy = self.policy.to_dict()

        paused = self.paused

        generation = self.generation

        observed_failures = self.observed_failures

        observed_completed_runs = self.observed_completed_runs

        pending_runs = self.pending_runs

        running_runs = self.running_runs

        waiting_runs = self.waiting_runs

        retained_events = self.retained_events

        history = []
        for history_item_data in self.history:
            history_item = history_item_data.to_dict()
            history.append(history_item)

        monitoring_since: str | Unset = UNSET
        if not isinstance(self.monitoring_since, Unset):
            monitoring_since = self.monitoring_since.isoformat()

        paused_at: str | Unset = UNSET
        if not isinstance(self.paused_at, Unset):
            paused_at = self.paused_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "policy": policy,
                "paused": paused,
                "generation": generation,
                "observed_failures": observed_failures,
                "observed_completed_runs": observed_completed_runs,
                "pending_runs": pending_runs,
                "running_runs": running_runs,
                "waiting_runs": waiting_runs,
                "retained_events": retained_events,
                "history": history,
            }
        )
        if monitoring_since is not UNSET:
            field_dict["monitoring_since"] = monitoring_since
        if paused_at is not UNSET:
            field_dict["paused_at"] = paused_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.automation_failure_policy import AutomationFailurePolicy
        from ..models.automation_failure_transition import AutomationFailureTransition

        d = dict(src_dict)
        policy = AutomationFailurePolicy.from_dict(d.pop("policy"))

        paused = d.pop("paused")

        generation = d.pop("generation")

        observed_failures = d.pop("observed_failures")

        observed_completed_runs = d.pop("observed_completed_runs")

        pending_runs = d.pop("pending_runs")

        running_runs = d.pop("running_runs")

        waiting_runs = d.pop("waiting_runs")

        retained_events = d.pop("retained_events")

        history = []
        _history = d.pop("history")
        for history_item_data in _history:
            history_item = AutomationFailureTransition.from_dict(history_item_data)

            history.append(history_item)

        _monitoring_since = d.pop("monitoring_since", UNSET)
        monitoring_since: datetime.datetime | Unset
        if isinstance(_monitoring_since, Unset):
            monitoring_since = UNSET
        else:
            monitoring_since = datetime.datetime.fromisoformat(_monitoring_since)

        _paused_at = d.pop("paused_at", UNSET)
        paused_at: datetime.datetime | Unset
        if isinstance(_paused_at, Unset):
            paused_at = UNSET
        else:
            paused_at = datetime.datetime.fromisoformat(_paused_at)

        automation_failure_policy_response = cls(
            policy=policy,
            paused=paused,
            generation=generation,
            observed_failures=observed_failures,
            observed_completed_runs=observed_completed_runs,
            pending_runs=pending_runs,
            running_runs=running_runs,
            waiting_runs=waiting_runs,
            retained_events=retained_events,
            history=history,
            monitoring_since=monitoring_since,
            paused_at=paused_at,
        )

        return automation_failure_policy_response
