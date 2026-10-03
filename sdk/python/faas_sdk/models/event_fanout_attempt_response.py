from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_fanout_attempt_response_action import (
    EventFanoutAttemptResponseAction,
    check_event_fanout_attempt_response_action,
)
from ..models.event_fanout_attempt_response_failure_code import (
    EventFanoutAttemptResponseFailureCode,
    check_event_fanout_attempt_response_failure_code,
)
from ..models.event_fanout_attempt_response_state import (
    EventFanoutAttemptResponseState,
    check_event_fanout_attempt_response_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EventFanoutAttemptResponse")


@_attrs_define
class EventFanoutAttemptResponse:
    """Immutable routing outcome or explicit operator replay request for one event recipient."""

    subscription_id: UUID
    action: EventFanoutAttemptResponseAction
    state: EventFanoutAttemptResponseState
    attempt_number: int
    retryable: bool
    occurred_at: datetime.datetime
    failure_code: EventFanoutAttemptResponseFailureCode | Unset = UNSET
    last_error: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        subscription_id = str(self.subscription_id)

        action: str = self.action

        state: str = self.state

        attempt_number = self.attempt_number

        retryable = self.retryable

        occurred_at = self.occurred_at.isoformat()

        failure_code: str | Unset = UNSET
        if not isinstance(self.failure_code, Unset):
            failure_code = self.failure_code

        last_error = self.last_error

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "subscription_id": subscription_id,
                "action": action,
                "state": state,
                "attempt_number": attempt_number,
                "retryable": retryable,
                "occurred_at": occurred_at,
            }
        )
        if failure_code is not UNSET:
            field_dict["failure_code"] = failure_code
        if last_error is not UNSET:
            field_dict["last_error"] = last_error

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        subscription_id = UUID(d.pop("subscription_id"))

        action = check_event_fanout_attempt_response_action(d.pop("action"))

        state = check_event_fanout_attempt_response_state(d.pop("state"))

        attempt_number = d.pop("attempt_number")

        retryable = d.pop("retryable")

        occurred_at = datetime.datetime.fromisoformat(d.pop("occurred_at"))

        _failure_code = d.pop("failure_code", UNSET)
        failure_code: EventFanoutAttemptResponseFailureCode | Unset
        if isinstance(_failure_code, Unset):
            failure_code = UNSET
        else:
            failure_code = check_event_fanout_attempt_response_failure_code(_failure_code)

        last_error = d.pop("last_error", UNSET)

        event_fanout_attempt_response = cls(
            subscription_id=subscription_id,
            action=action,
            state=state,
            attempt_number=attempt_number,
            retryable=retryable,
            occurred_at=occurred_at,
            failure_code=failure_code,
            last_error=last_error,
        )

        return event_fanout_attempt_response
