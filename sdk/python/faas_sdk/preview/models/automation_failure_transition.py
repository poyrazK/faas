from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.automation_failure_transition_reason import (
    AutomationFailureTransitionReason,
    check_automation_failure_transition_reason,
)
from ..models.automation_failure_transition_state import (
    AutomationFailureTransitionState,
    check_automation_failure_transition_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="AutomationFailureTransition")


@_attrs_define
class AutomationFailureTransition:
    """Immutable bounded aggregate evidence for a failure guard transition."""

    generation: int
    """Unique runtime transition generation for this automation."""
    state: AutomationFailureTransitionState
    """Failure guard state resulting from this transition."""
    reason: AutomationFailureTransitionReason
    """Closed reason for the recorded transition."""
    recorded_at: datetime.datetime
    """Server timestamp of the pause or operator resume."""
    failures: int
    """Observed failures recorded with this transition."""
    completed_runs: int
    """Observed completed sample count recorded with this transition."""
    policy_version: int
    """Failure monitoring policy revision used for this transition."""
    actor_account_id: str | Unset = UNSET
    """Account that explicitly resumed; empty or omitted for an automatic pause."""

    def to_dict(self) -> dict[str, Any]:
        generation = self.generation

        state: str = self.state

        reason: str = self.reason

        recorded_at = self.recorded_at.isoformat()

        failures = self.failures

        completed_runs = self.completed_runs

        policy_version = self.policy_version

        actor_account_id = self.actor_account_id

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "generation": generation,
                "state": state,
                "reason": reason,
                "recorded_at": recorded_at,
                "failures": failures,
                "completed_runs": completed_runs,
                "policy_version": policy_version,
            }
        )
        if actor_account_id is not UNSET:
            field_dict["actor_account_id"] = actor_account_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        generation = d.pop("generation")

        state = check_automation_failure_transition_state(d.pop("state"))

        reason = check_automation_failure_transition_reason(d.pop("reason"))

        recorded_at = datetime.datetime.fromisoformat(d.pop("recorded_at"))

        failures = d.pop("failures")

        completed_runs = d.pop("completed_runs")

        policy_version = d.pop("policy_version")

        actor_account_id = d.pop("actor_account_id", UNSET)

        automation_failure_transition = cls(
            generation=generation,
            state=state,
            reason=reason,
            recorded_at=recorded_at,
            failures=failures,
            completed_runs=completed_runs,
            policy_version=policy_version,
            actor_account_id=actor_account_id,
        )

        return automation_failure_transition
