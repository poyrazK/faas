from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_receipt_execution_response_state import (
    EventReceiptExecutionResponseState,
    check_event_receipt_execution_response_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EventReceiptExecutionResponse")


@_attrs_define
class EventReceiptExecutionResponse:
    """Current retained invocation state. The recipient execution is the original deterministic invocation; recovery and
    history contain trusted generic replay invocations.

    """

    invocation_id: UUID
    state: EventReceiptExecutionResponseState
    attempts: int
    replay_generation: int
    """This invocation's budget generation, updated by in-place dead-letter replay."""
    created_at: datetime.datetime
    replayed_from_invocation_id: UUID | Unset = UNSET
    """Trusted immediate parent for a generic replay; its execution record may have expired."""
    next_attempt_at: datetime.datetime | Unset = UNSET
    """Next pending invocation due time; dispatch may be delayed by admission."""
    completed_at: datetime.datetime | Unset = UNSET
    last_error: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        invocation_id = str(self.invocation_id)

        state: str = self.state

        attempts = self.attempts

        replay_generation = self.replay_generation

        created_at = self.created_at.isoformat()

        replayed_from_invocation_id: str | Unset = UNSET
        if not isinstance(self.replayed_from_invocation_id, Unset):
            replayed_from_invocation_id = str(self.replayed_from_invocation_id)

        next_attempt_at: str | Unset = UNSET
        if not isinstance(self.next_attempt_at, Unset):
            next_attempt_at = self.next_attempt_at.isoformat()

        completed_at: str | Unset = UNSET
        if not isinstance(self.completed_at, Unset):
            completed_at = self.completed_at.isoformat()

        last_error = self.last_error

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "invocation_id": invocation_id,
                "state": state,
                "attempts": attempts,
                "replay_generation": replay_generation,
                "created_at": created_at,
            }
        )
        if replayed_from_invocation_id is not UNSET:
            field_dict["replayed_from_invocation_id"] = replayed_from_invocation_id
        if next_attempt_at is not UNSET:
            field_dict["next_attempt_at"] = next_attempt_at
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at
        if last_error is not UNSET:
            field_dict["last_error"] = last_error

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        invocation_id = UUID(d.pop("invocation_id"))

        state = check_event_receipt_execution_response_state(d.pop("state"))

        attempts = d.pop("attempts")

        replay_generation = d.pop("replay_generation")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        _replayed_from_invocation_id = d.pop("replayed_from_invocation_id", UNSET)
        replayed_from_invocation_id: UUID | Unset
        if isinstance(_replayed_from_invocation_id, Unset):
            replayed_from_invocation_id = UNSET
        else:
            replayed_from_invocation_id = UUID(_replayed_from_invocation_id)

        _next_attempt_at = d.pop("next_attempt_at", UNSET)
        next_attempt_at: datetime.datetime | Unset
        if isinstance(_next_attempt_at, Unset):
            next_attempt_at = UNSET
        else:
            next_attempt_at = datetime.datetime.fromisoformat(_next_attempt_at)

        _completed_at = d.pop("completed_at", UNSET)
        completed_at: datetime.datetime | Unset
        if isinstance(_completed_at, Unset):
            completed_at = UNSET
        else:
            completed_at = datetime.datetime.fromisoformat(_completed_at)

        last_error = d.pop("last_error", UNSET)

        event_receipt_execution_response = cls(
            invocation_id=invocation_id,
            state=state,
            attempts=attempts,
            replay_generation=replay_generation,
            created_at=created_at,
            replayed_from_invocation_id=replayed_from_invocation_id,
            next_attempt_at=next_attempt_at,
            completed_at=completed_at,
            last_error=last_error,
        )

        return event_receipt_execution_response
