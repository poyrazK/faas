from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_timeline_attempt_outcome import (
    OperationTimelineAttemptOutcome,
    check_operation_timeline_attempt_outcome,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_attempt_logs import OperationAttemptLogs


T = TypeVar("T", bound="OperationTimelineAttempt")


@_attrs_define
class OperationTimelineAttempt:
    """One HTTP execution or workflow step attempt as read; a running attempt is not re-emitted when it ends."""

    execution_id: UUID
    attempt: int
    outcome: OperationTimelineAttemptOutcome
    started_at: datetime.datetime
    generation: int | Unset = UNSET
    """Execution generation of an HTTP attempt. Step attempts omit it because recovery resumes the same workflow
    run."""
    step: str | Unset = UNSET
    http_status: int | Unset = UNSET
    error: str | Unset = UNSET
    finished_at: datetime.datetime | Unset = UNSET
    next_attempt_at: datetime.datetime | Unset = UNSET
    logs: OperationAttemptLogs | Unset = UNSET
    """Instance log window of an HTTP attempt. Lines other requests wrote on the same instance during the window
    interleave."""

    def to_dict(self) -> dict[str, Any]:
        execution_id = str(self.execution_id)

        attempt = self.attempt

        outcome: str = self.outcome

        started_at = self.started_at.isoformat()

        generation = self.generation

        step = self.step

        http_status = self.http_status

        error = self.error

        finished_at: str | Unset = UNSET
        if not isinstance(self.finished_at, Unset):
            finished_at = self.finished_at.isoformat()

        next_attempt_at: str | Unset = UNSET
        if not isinstance(self.next_attempt_at, Unset):
            next_attempt_at = self.next_attempt_at.isoformat()

        logs: dict[str, Any] | Unset = UNSET
        if not isinstance(self.logs, Unset):
            logs = self.logs.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "execution_id": execution_id,
                "attempt": attempt,
                "outcome": outcome,
                "started_at": started_at,
            }
        )
        if generation is not UNSET:
            field_dict["generation"] = generation
        if step is not UNSET:
            field_dict["step"] = step
        if http_status is not UNSET:
            field_dict["http_status"] = http_status
        if error is not UNSET:
            field_dict["error"] = error
        if finished_at is not UNSET:
            field_dict["finished_at"] = finished_at
        if next_attempt_at is not UNSET:
            field_dict["next_attempt_at"] = next_attempt_at
        if logs is not UNSET:
            field_dict["logs"] = logs

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_attempt_logs import OperationAttemptLogs

        d = dict(src_dict)
        execution_id = UUID(d.pop("execution_id"))

        attempt = d.pop("attempt")

        outcome = check_operation_timeline_attempt_outcome(d.pop("outcome"))

        started_at = datetime.datetime.fromisoformat(d.pop("started_at"))

        generation = d.pop("generation", UNSET)

        step = d.pop("step", UNSET)

        http_status = d.pop("http_status", UNSET)

        error = d.pop("error", UNSET)

        _finished_at = d.pop("finished_at", UNSET)
        finished_at: datetime.datetime | Unset
        if isinstance(_finished_at, Unset):
            finished_at = UNSET
        else:
            finished_at = datetime.datetime.fromisoformat(_finished_at)

        _next_attempt_at = d.pop("next_attempt_at", UNSET)
        next_attempt_at: datetime.datetime | Unset
        if isinstance(_next_attempt_at, Unset):
            next_attempt_at = UNSET
        else:
            next_attempt_at = datetime.datetime.fromisoformat(_next_attempt_at)

        _logs = d.pop("logs", UNSET)
        logs: OperationAttemptLogs | Unset
        if isinstance(_logs, Unset):
            logs = UNSET
        else:
            logs = OperationAttemptLogs.from_dict(_logs)

        operation_timeline_attempt = cls(
            execution_id=execution_id,
            attempt=attempt,
            outcome=outcome,
            started_at=started_at,
            generation=generation,
            step=step,
            http_status=http_status,
            error=error,
            finished_at=finished_at,
            next_attempt_at=next_attempt_at,
            logs=logs,
        )

        return operation_timeline_attempt
