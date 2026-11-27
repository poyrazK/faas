from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.stored_workflow_simulation_attempt_outcome import (
    StoredWorkflowSimulationAttemptOutcome,
    check_stored_workflow_simulation_attempt_outcome,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="StoredWorkflowSimulationAttempt")


@_attrs_define
class StoredWorkflowSimulationAttempt:
    """Safe summary of one supplied hypothetical attempt outcome."""

    attempt: int
    outcome: StoredWorkflowSimulationAttemptOutcome
    http_status: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        attempt = self.attempt

        outcome: str = self.outcome

        http_status = self.http_status

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "attempt": attempt,
                "outcome": outcome,
            }
        )
        if http_status is not UNSET:
            field_dict["http_status"] = http_status

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        attempt = d.pop("attempt")

        outcome = check_stored_workflow_simulation_attempt_outcome(d.pop("outcome"))

        http_status = d.pop("http_status", UNSET)

        stored_workflow_simulation_attempt = cls(
            attempt=attempt,
            outcome=outcome,
            http_status=http_status,
        )

        return stored_workflow_simulation_attempt
