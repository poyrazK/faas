from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.stored_workflow_simulation_mock_attempt_outcome import (
    StoredWorkflowSimulationMockAttemptOutcome,
    check_stored_workflow_simulation_mock_attempt_outcome,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="StoredWorkflowSimulationMockAttempt")


@_attrs_define
class StoredWorkflowSimulationMockAttempt:
    """One mocked attempt; success requires output, failure requires exactly one of error or non-2xx http_status, and
    timeout has no additional fields.

    """

    outcome: StoredWorkflowSimulationMockAttemptOutcome
    output: Any | Unset = UNSET
    """Successful action result or received event/callback payload; any JSON value, including null."""
    error: str | Unset = UNSET
    """Mocked transport or action error message used as failure.message."""
    http_status: int | Unset = UNSET
    """Mocked non-2xx response status."""

    def to_dict(self) -> dict[str, Any]:
        outcome: str = self.outcome

        output = self.output

        error = self.error

        http_status = self.http_status

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "outcome": outcome,
            }
        )
        if output is not UNSET:
            field_dict["output"] = output
        if error is not UNSET:
            field_dict["error"] = error
        if http_status is not UNSET:
            field_dict["http_status"] = http_status

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        outcome = check_stored_workflow_simulation_mock_attempt_outcome(d.pop("outcome"))

        output = d.pop("output", UNSET)

        error = d.pop("error", UNSET)

        http_status = d.pop("http_status", UNSET)

        stored_workflow_simulation_mock_attempt = cls(
            outcome=outcome,
            output=output,
            error=error,
            http_status=http_status,
        )

        return stored_workflow_simulation_mock_attempt
