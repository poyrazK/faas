from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.invocation_attempt_response_outcome import (
    InvocationAttemptResponseOutcome,
    check_invocation_attempt_response_outcome,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="InvocationAttemptResponse")


@_attrs_define
class InvocationAttemptResponse:
    id: int
    invocation_id: UUID
    replay_generation: int
    attempt: int
    started_at: datetime.datetime
    outcome: InvocationAttemptResponseOutcome
    """Dispatch evidence; unknown does not prove whether the handler ran."""
    retain_until: datetime.datetime
    """Closed attempts can expire earlier if their invocation is deleted; running attempts are not pruned."""
    finished_at: datetime.datetime | Unset = UNSET
    error_detail: str | Unset = UNSET
    next_attempt_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        invocation_id = str(self.invocation_id)

        replay_generation = self.replay_generation

        attempt = self.attempt

        started_at = self.started_at.isoformat()

        outcome: str = self.outcome

        retain_until = self.retain_until.isoformat()

        finished_at: str | Unset = UNSET
        if not isinstance(self.finished_at, Unset):
            finished_at = self.finished_at.isoformat()

        error_detail = self.error_detail

        next_attempt_at: str | Unset = UNSET
        if not isinstance(self.next_attempt_at, Unset):
            next_attempt_at = self.next_attempt_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "invocation_id": invocation_id,
                "replay_generation": replay_generation,
                "attempt": attempt,
                "started_at": started_at,
                "outcome": outcome,
                "retain_until": retain_until,
            }
        )
        if finished_at is not UNSET:
            field_dict["finished_at"] = finished_at
        if error_detail is not UNSET:
            field_dict["error_detail"] = error_detail
        if next_attempt_at is not UNSET:
            field_dict["next_attempt_at"] = next_attempt_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        invocation_id = UUID(d.pop("invocation_id"))

        replay_generation = d.pop("replay_generation")

        attempt = d.pop("attempt")

        started_at = datetime.datetime.fromisoformat(d.pop("started_at"))

        outcome = check_invocation_attempt_response_outcome(d.pop("outcome"))

        retain_until = datetime.datetime.fromisoformat(d.pop("retain_until"))

        _finished_at = d.pop("finished_at", UNSET)
        finished_at: datetime.datetime | Unset
        if isinstance(_finished_at, Unset):
            finished_at = UNSET
        else:
            finished_at = datetime.datetime.fromisoformat(_finished_at)

        error_detail = d.pop("error_detail", UNSET)

        _next_attempt_at = d.pop("next_attempt_at", UNSET)
        next_attempt_at: datetime.datetime | Unset
        if isinstance(_next_attempt_at, Unset):
            next_attempt_at = UNSET
        else:
            next_attempt_at = datetime.datetime.fromisoformat(_next_attempt_at)

        invocation_attempt_response = cls(
            id=id,
            invocation_id=invocation_id,
            replay_generation=replay_generation,
            attempt=attempt,
            started_at=started_at,
            outcome=outcome,
            retain_until=retain_until,
            finished_at=finished_at,
            error_detail=error_detail,
            next_attempt_at=next_attempt_at,
        )

        return invocation_attempt_response
