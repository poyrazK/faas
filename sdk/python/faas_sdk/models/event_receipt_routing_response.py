from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.event_receipt_routing_response_capacity_scope import (
    EventReceiptRoutingResponseCapacityScope,
    check_event_receipt_routing_response_capacity_scope,
)
from ..models.event_receipt_routing_response_filter_reason import (
    EventReceiptRoutingResponseFilterReason,
    check_event_receipt_routing_response_filter_reason,
)
from ..models.event_receipt_routing_response_retry_stop_reason import (
    EventReceiptRoutingResponseRetryStopReason,
    check_event_receipt_routing_response_retry_stop_reason,
)
from ..models.event_receipt_routing_response_state import (
    EventReceiptRoutingResponseState,
    check_event_receipt_routing_response_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_routing_retry_policy import EventRoutingRetryPolicy


T = TypeVar("T", bound="EventReceiptRoutingResponse")


@_attrs_define
class EventReceiptRoutingResponse:
    """Recipient routing checkpoint and lifetime attempts; execution attempts are separate."""

    state: EventReceiptRoutingResponseState
    attempts: int
    """Lifetime routing attempts, including replay generations."""
    retryable: bool
    replay_count: int
    delivery_deadline_at: datetime.datetime | Unset = UNSET
    """Captured wall-clock routing deadline."""
    delivery_age_override: bool | Unset = UNSET
    """Explicit operator override recorded for the current replay generation or backfill."""
    routing_retry_policy: EventRoutingRetryPolicy | Unset = UNSET
    """Routing policy before invocation admission. Duration budgets include routing attempt time and scheduled
    retry delays. Admission waits add no budget cost. API replacement accepts explicit settings; manifests and CLI
    default configured policies to jitter enabled."""
    filter_reason: EventReceiptRoutingResponseFilterReason | Unset = UNSET
    """Why routing was filtered without consuming a routing attempt."""
    retry_stop_reason: EventReceiptRoutingResponseRetryStopReason | Unset = UNSET
    """Why automatic routing retries stopped; failure_code retains the underlying cause."""
    retry_spent_ms: int | Unset = UNSET
    """Current replay generation duration budget spent on routing attempts and scheduled delays."""
    generation_capacity_deferrals: int | Unset = UNSET
    """Capacity waits in this independent routing generation; subtract from generation_attempts for failure-budget
    use."""
    capacity_deferrals: int | Unset = UNSET
    """Lifetime capacity waits; excluded from the routing failure budget."""
    capacity_scope: EventReceiptRoutingResponseCapacityScope | Unset = UNSET
    """Active capacity wait reason; absent after admission."""
    pending_age_seconds: float | Unset = UNSET
    """Age since event acceptance while routing is pending or processing."""
    generation: int | Unset = UNSET
    """Independent routing generation; absent on legacy whole-event receipts."""
    generation_attempts: int | Unset = UNSET
    """Claims in the current independent generation, including capacity waits; subtract
    generation_capacity_deferrals for failure-budget use."""
    next_attempt_at: datetime.datetime | Unset = UNSET
    """Scheduled pending routing retry, not a dispatch guarantee."""
    lease_until: datetime.datetime | Unset = UNSET
    """Independent routing claim expiry; no claim token is exposed."""
    updated_at: datetime.datetime | Unset = UNSET
    last_error: str | Unset = UNSET
    failure_code: str | Unset = UNSET
    last_replayed_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        attempts = self.attempts

        retryable = self.retryable

        replay_count = self.replay_count

        delivery_deadline_at: str | Unset = UNSET
        if not isinstance(self.delivery_deadline_at, Unset):
            delivery_deadline_at = self.delivery_deadline_at.isoformat()

        delivery_age_override = self.delivery_age_override

        routing_retry_policy: dict[str, Any] | Unset = UNSET
        if not isinstance(self.routing_retry_policy, Unset):
            routing_retry_policy = self.routing_retry_policy.to_dict()

        filter_reason: str | Unset = UNSET
        if not isinstance(self.filter_reason, Unset):
            filter_reason = self.filter_reason

        retry_stop_reason: str | Unset = UNSET
        if not isinstance(self.retry_stop_reason, Unset):
            retry_stop_reason = self.retry_stop_reason

        retry_spent_ms = self.retry_spent_ms

        generation_capacity_deferrals = self.generation_capacity_deferrals

        capacity_deferrals = self.capacity_deferrals

        capacity_scope: str | Unset = UNSET
        if not isinstance(self.capacity_scope, Unset):
            capacity_scope = self.capacity_scope

        pending_age_seconds = self.pending_age_seconds

        generation = self.generation

        generation_attempts = self.generation_attempts

        next_attempt_at: str | Unset = UNSET
        if not isinstance(self.next_attempt_at, Unset):
            next_attempt_at = self.next_attempt_at.isoformat()

        lease_until: str | Unset = UNSET
        if not isinstance(self.lease_until, Unset):
            lease_until = self.lease_until.isoformat()

        updated_at: str | Unset = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        last_error = self.last_error

        failure_code = self.failure_code

        last_replayed_at: str | Unset = UNSET
        if not isinstance(self.last_replayed_at, Unset):
            last_replayed_at = self.last_replayed_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "state": state,
                "attempts": attempts,
                "retryable": retryable,
                "replay_count": replay_count,
            }
        )
        if delivery_deadline_at is not UNSET:
            field_dict["delivery_deadline_at"] = delivery_deadline_at
        if delivery_age_override is not UNSET:
            field_dict["delivery_age_override"] = delivery_age_override
        if routing_retry_policy is not UNSET:
            field_dict["routing_retry_policy"] = routing_retry_policy
        if filter_reason is not UNSET:
            field_dict["filter_reason"] = filter_reason
        if retry_stop_reason is not UNSET:
            field_dict["retry_stop_reason"] = retry_stop_reason
        if retry_spent_ms is not UNSET:
            field_dict["retry_spent_ms"] = retry_spent_ms
        if generation_capacity_deferrals is not UNSET:
            field_dict["generation_capacity_deferrals"] = generation_capacity_deferrals
        if capacity_deferrals is not UNSET:
            field_dict["capacity_deferrals"] = capacity_deferrals
        if capacity_scope is not UNSET:
            field_dict["capacity_scope"] = capacity_scope
        if pending_age_seconds is not UNSET:
            field_dict["pending_age_seconds"] = pending_age_seconds
        if generation is not UNSET:
            field_dict["generation"] = generation
        if generation_attempts is not UNSET:
            field_dict["generation_attempts"] = generation_attempts
        if next_attempt_at is not UNSET:
            field_dict["next_attempt_at"] = next_attempt_at
        if lease_until is not UNSET:
            field_dict["lease_until"] = lease_until
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at
        if last_error is not UNSET:
            field_dict["last_error"] = last_error
        if failure_code is not UNSET:
            field_dict["failure_code"] = failure_code
        if last_replayed_at is not UNSET:
            field_dict["last_replayed_at"] = last_replayed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_routing_retry_policy import EventRoutingRetryPolicy

        d = dict(src_dict)
        state = check_event_receipt_routing_response_state(d.pop("state"))

        attempts = d.pop("attempts")

        retryable = d.pop("retryable")

        replay_count = d.pop("replay_count")

        _delivery_deadline_at = d.pop("delivery_deadline_at", UNSET)
        delivery_deadline_at: datetime.datetime | Unset
        if isinstance(_delivery_deadline_at, Unset):
            delivery_deadline_at = UNSET
        else:
            delivery_deadline_at = datetime.datetime.fromisoformat(_delivery_deadline_at)

        delivery_age_override = d.pop("delivery_age_override", UNSET)

        _routing_retry_policy = d.pop("routing_retry_policy", UNSET)
        routing_retry_policy: EventRoutingRetryPolicy | Unset
        if isinstance(_routing_retry_policy, Unset):
            routing_retry_policy = UNSET
        else:
            routing_retry_policy = EventRoutingRetryPolicy.from_dict(_routing_retry_policy)

        _filter_reason = d.pop("filter_reason", UNSET)
        filter_reason: EventReceiptRoutingResponseFilterReason | Unset
        if isinstance(_filter_reason, Unset):
            filter_reason = UNSET
        else:
            filter_reason = check_event_receipt_routing_response_filter_reason(_filter_reason)

        _retry_stop_reason = d.pop("retry_stop_reason", UNSET)
        retry_stop_reason: EventReceiptRoutingResponseRetryStopReason | Unset
        if isinstance(_retry_stop_reason, Unset):
            retry_stop_reason = UNSET
        else:
            retry_stop_reason = check_event_receipt_routing_response_retry_stop_reason(_retry_stop_reason)

        retry_spent_ms = d.pop("retry_spent_ms", UNSET)

        generation_capacity_deferrals = d.pop("generation_capacity_deferrals", UNSET)

        capacity_deferrals = d.pop("capacity_deferrals", UNSET)

        _capacity_scope = d.pop("capacity_scope", UNSET)
        capacity_scope: EventReceiptRoutingResponseCapacityScope | Unset
        if isinstance(_capacity_scope, Unset):
            capacity_scope = UNSET
        else:
            capacity_scope = check_event_receipt_routing_response_capacity_scope(_capacity_scope)

        pending_age_seconds = d.pop("pending_age_seconds", UNSET)

        generation = d.pop("generation", UNSET)

        generation_attempts = d.pop("generation_attempts", UNSET)

        _next_attempt_at = d.pop("next_attempt_at", UNSET)
        next_attempt_at: datetime.datetime | Unset
        if isinstance(_next_attempt_at, Unset):
            next_attempt_at = UNSET
        else:
            next_attempt_at = datetime.datetime.fromisoformat(_next_attempt_at)

        _lease_until = d.pop("lease_until", UNSET)
        lease_until: datetime.datetime | Unset
        if isinstance(_lease_until, Unset):
            lease_until = UNSET
        else:
            lease_until = datetime.datetime.fromisoformat(_lease_until)

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: datetime.datetime | Unset
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = datetime.datetime.fromisoformat(_updated_at)

        last_error = d.pop("last_error", UNSET)

        failure_code = d.pop("failure_code", UNSET)

        _last_replayed_at = d.pop("last_replayed_at", UNSET)
        last_replayed_at: datetime.datetime | Unset
        if isinstance(_last_replayed_at, Unset):
            last_replayed_at = UNSET
        else:
            last_replayed_at = datetime.datetime.fromisoformat(_last_replayed_at)

        event_receipt_routing_response = cls(
            state=state,
            attempts=attempts,
            retryable=retryable,
            replay_count=replay_count,
            delivery_deadline_at=delivery_deadline_at,
            delivery_age_override=delivery_age_override,
            routing_retry_policy=routing_retry_policy,
            filter_reason=filter_reason,
            retry_stop_reason=retry_stop_reason,
            retry_spent_ms=retry_spent_ms,
            generation_capacity_deferrals=generation_capacity_deferrals,
            capacity_deferrals=capacity_deferrals,
            capacity_scope=capacity_scope,
            pending_age_seconds=pending_age_seconds,
            generation=generation,
            generation_attempts=generation_attempts,
            next_attempt_at=next_attempt_at,
            lease_until=lease_until,
            updated_at=updated_at,
            last_error=last_error,
            failure_code=failure_code,
            last_replayed_at=last_replayed_at,
        )

        return event_receipt_routing_response
