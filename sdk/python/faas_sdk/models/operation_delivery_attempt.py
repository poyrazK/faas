from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_delivery_attempt_outcome import (
    OperationDeliveryAttemptOutcome,
    check_operation_delivery_attempt_outcome,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationDeliveryAttempt")


@_attrs_define
class OperationDeliveryAttempt:
    """One retained notification attempt without receiver payload, URL or raw transport errors."""

    replay_generation: int
    attempt_number: int
    outcome: OperationDeliveryAttemptOutcome
    response_code: int
    started_at: datetime.datetime
    finished_at: datetime.datetime
    error_code: str | Unset = UNSET
    next_attempt_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        replay_generation = self.replay_generation

        attempt_number = self.attempt_number

        outcome: str = self.outcome

        response_code = self.response_code

        started_at = self.started_at.isoformat()

        finished_at = self.finished_at.isoformat()

        error_code = self.error_code

        next_attempt_at: str | Unset = UNSET
        if not isinstance(self.next_attempt_at, Unset):
            next_attempt_at = self.next_attempt_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "replay_generation": replay_generation,
                "attempt_number": attempt_number,
                "outcome": outcome,
                "response_code": response_code,
                "started_at": started_at,
                "finished_at": finished_at,
            }
        )
        if error_code is not UNSET:
            field_dict["error_code"] = error_code
        if next_attempt_at is not UNSET:
            field_dict["next_attempt_at"] = next_attempt_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        replay_generation = d.pop("replay_generation")

        attempt_number = d.pop("attempt_number")

        outcome = check_operation_delivery_attempt_outcome(d.pop("outcome"))

        response_code = d.pop("response_code")

        started_at = datetime.datetime.fromisoformat(d.pop("started_at"))

        finished_at = datetime.datetime.fromisoformat(d.pop("finished_at"))

        error_code = d.pop("error_code", UNSET)

        _next_attempt_at = d.pop("next_attempt_at", UNSET)
        next_attempt_at: datetime.datetime | Unset
        if isinstance(_next_attempt_at, Unset):
            next_attempt_at = UNSET
        else:
            next_attempt_at = datetime.datetime.fromisoformat(_next_attempt_at)

        operation_delivery_attempt = cls(
            replay_generation=replay_generation,
            attempt_number=attempt_number,
            outcome=outcome,
            response_code=response_code,
            started_at=started_at,
            finished_at=finished_at,
            error_code=error_code,
            next_attempt_at=next_attempt_at,
        )

        return operation_delivery_attempt
