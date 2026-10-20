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
from ..models.event_fanout_attempt_response_capacity_scope import (
    EventFanoutAttemptResponseCapacityScope,
    check_event_fanout_attempt_response_capacity_scope,
)
from ..models.event_fanout_attempt_response_failure_code import (
    EventFanoutAttemptResponseFailureCode,
    check_event_fanout_attempt_response_failure_code,
)
from ..models.event_fanout_attempt_response_filter_reason import (
    EventFanoutAttemptResponseFilterReason,
    check_event_fanout_attempt_response_filter_reason,
)
from ..models.event_fanout_attempt_response_retry_stop_reason import (
    EventFanoutAttemptResponseRetryStopReason,
    check_event_fanout_attempt_response_retry_stop_reason,
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
    filter_reason: EventFanoutAttemptResponseFilterReason | Unset = UNSET
    """Why routing was filtered without consuming a routing attempt."""
    retry_stop_reason: EventFanoutAttemptResponseRetryStopReason | Unset = UNSET
    """Why automatic routing retries stopped; failure_code retains the underlying cause."""
    failure_code: EventFanoutAttemptResponseFailureCode | Unset = UNSET
    last_error: str | Unset = UNSET
    capacity_scope: EventFanoutAttemptResponseCapacityScope | Unset = UNSET
    """Capacity wait scope for this immutable observation."""
    capacity_deferrals: int | Unset = UNSET
    """Cumulative deferrals at this observation; summaries provide the current total."""
    details_truncated: bool | Unset = UNSET
    """Error or failure code detail was truncated to its UTF-8 byte ceiling."""

    def to_dict(self) -> dict[str, Any]:
        subscription_id = str(self.subscription_id)

        action: str = self.action

        state: str = self.state

        attempt_number = self.attempt_number

        retryable = self.retryable

        occurred_at = self.occurred_at.isoformat()

        filter_reason: str | Unset = UNSET
        if not isinstance(self.filter_reason, Unset):
            filter_reason = self.filter_reason

        retry_stop_reason: str | Unset = UNSET
        if not isinstance(self.retry_stop_reason, Unset):
            retry_stop_reason = self.retry_stop_reason

        failure_code: str | Unset = UNSET
        if not isinstance(self.failure_code, Unset):
            failure_code = self.failure_code

        last_error = self.last_error

        capacity_scope: str | Unset = UNSET
        if not isinstance(self.capacity_scope, Unset):
            capacity_scope = self.capacity_scope

        capacity_deferrals = self.capacity_deferrals

        details_truncated = self.details_truncated

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
        if filter_reason is not UNSET:
            field_dict["filter_reason"] = filter_reason
        if retry_stop_reason is not UNSET:
            field_dict["retry_stop_reason"] = retry_stop_reason
        if failure_code is not UNSET:
            field_dict["failure_code"] = failure_code
        if last_error is not UNSET:
            field_dict["last_error"] = last_error
        if capacity_scope is not UNSET:
            field_dict["capacity_scope"] = capacity_scope
        if capacity_deferrals is not UNSET:
            field_dict["capacity_deferrals"] = capacity_deferrals
        if details_truncated is not UNSET:
            field_dict["details_truncated"] = details_truncated

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

        _filter_reason = d.pop("filter_reason", UNSET)
        filter_reason: EventFanoutAttemptResponseFilterReason | Unset
        if isinstance(_filter_reason, Unset):
            filter_reason = UNSET
        else:
            filter_reason = check_event_fanout_attempt_response_filter_reason(_filter_reason)

        _retry_stop_reason = d.pop("retry_stop_reason", UNSET)
        retry_stop_reason: EventFanoutAttemptResponseRetryStopReason | Unset
        if isinstance(_retry_stop_reason, Unset):
            retry_stop_reason = UNSET
        else:
            retry_stop_reason = check_event_fanout_attempt_response_retry_stop_reason(_retry_stop_reason)

        _failure_code = d.pop("failure_code", UNSET)
        failure_code: EventFanoutAttemptResponseFailureCode | Unset
        if isinstance(_failure_code, Unset):
            failure_code = UNSET
        else:
            failure_code = check_event_fanout_attempt_response_failure_code(_failure_code)

        last_error = d.pop("last_error", UNSET)

        _capacity_scope = d.pop("capacity_scope", UNSET)
        capacity_scope: EventFanoutAttemptResponseCapacityScope | Unset
        if isinstance(_capacity_scope, Unset):
            capacity_scope = UNSET
        else:
            capacity_scope = check_event_fanout_attempt_response_capacity_scope(_capacity_scope)

        capacity_deferrals = d.pop("capacity_deferrals", UNSET)

        details_truncated = d.pop("details_truncated", UNSET)

        event_fanout_attempt_response = cls(
            subscription_id=subscription_id,
            action=action,
            state=state,
            attempt_number=attempt_number,
            retryable=retryable,
            occurred_at=occurred_at,
            filter_reason=filter_reason,
            retry_stop_reason=retry_stop_reason,
            failure_code=failure_code,
            last_error=last_error,
            capacity_scope=capacity_scope,
            capacity_deferrals=capacity_deferrals,
            details_truncated=details_truncated,
        )

        return event_fanout_attempt_response
