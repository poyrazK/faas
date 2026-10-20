from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_fanout_failure_response_failure_code import (
    EventFanoutFailureResponseFailureCode,
    check_event_fanout_failure_response_failure_code,
)
from ..models.event_fanout_failure_response_state import (
    EventFanoutFailureResponseState,
    check_event_fanout_failure_response_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EventFanoutFailureResponse")


@_attrs_define
class EventFanoutFailureResponse:
    """A terminal subscription routing failure before an invocation was created."""

    event_id: str
    event_source: str
    event_type: str
    subscription_id: UUID
    state: EventFanoutFailureResponseState
    attempts: int
    last_error: str
    created_at: datetime.datetime
    """When the event was accepted."""
    failed_at: datetime.datetime
    """When recipient routing became terminal."""
    failure_code: EventFanoutFailureResponseFailureCode | Unset = UNSET
    """Stable routing failure category; unknown covers failures recorded before classification was available.
    Optional for responses from older apid versions during rollout."""
    retryable: bool | Unset = UNSET
    """True when the failure was caused by a transient lookup or enqueue error and replay may succeed without
    changing subscription configuration. Optional for responses from older apid versions during rollout."""

    def to_dict(self) -> dict[str, Any]:
        event_id = self.event_id

        event_source = self.event_source

        event_type = self.event_type

        subscription_id = str(self.subscription_id)

        state: str = self.state

        attempts = self.attempts

        last_error = self.last_error

        created_at = self.created_at.isoformat()

        failed_at = self.failed_at.isoformat()

        failure_code: str | Unset = UNSET
        if not isinstance(self.failure_code, Unset):
            failure_code = self.failure_code

        retryable = self.retryable

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "event_id": event_id,
                "event_source": event_source,
                "event_type": event_type,
                "subscription_id": subscription_id,
                "state": state,
                "attempts": attempts,
                "last_error": last_error,
                "created_at": created_at,
                "failed_at": failed_at,
            }
        )
        if failure_code is not UNSET:
            field_dict["failure_code"] = failure_code
        if retryable is not UNSET:
            field_dict["retryable"] = retryable

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        event_id = d.pop("event_id")

        event_source = d.pop("event_source")

        event_type = d.pop("event_type")

        subscription_id = UUID(d.pop("subscription_id"))

        state = check_event_fanout_failure_response_state(d.pop("state"))

        attempts = d.pop("attempts")

        last_error = d.pop("last_error")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        failed_at = datetime.datetime.fromisoformat(d.pop("failed_at"))

        _failure_code = d.pop("failure_code", UNSET)
        failure_code: EventFanoutFailureResponseFailureCode | Unset
        if isinstance(_failure_code, Unset):
            failure_code = UNSET
        else:
            failure_code = check_event_fanout_failure_response_failure_code(_failure_code)

        retryable = d.pop("retryable", UNSET)

        event_fanout_failure_response = cls(
            event_id=event_id,
            event_source=event_source,
            event_type=event_type,
            subscription_id=subscription_id,
            state=state,
            attempts=attempts,
            last_error=last_error,
            created_at=created_at,
            failed_at=failed_at,
            failure_code=failure_code,
            retryable=retryable,
        )

        return event_fanout_failure_response
