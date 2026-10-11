from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_backlog_recipient_capacity_scope import (
    EventBacklogRecipientCapacityScope,
    check_event_backlog_recipient_capacity_scope,
)
from ..models.event_backlog_recipient_routing_mode import (
    EventBacklogRecipientRoutingMode,
    check_event_backlog_recipient_routing_mode,
)
from ..models.event_backlog_recipient_state import EventBacklogRecipientState, check_event_backlog_recipient_state
from ..models.event_backlog_recipient_waiting_reason import (
    EventBacklogRecipientWaitingReason,
    check_event_backlog_recipient_waiting_reason,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EventBacklogRecipient")


@_attrs_define
class EventBacklogRecipient:
    """One captured application recipient awaiting routing, without envelope data."""

    event_source: str
    event_id: str
    event_type: str
    accepted_at: datetime.datetime
    app_id: UUID
    app_slug: str
    """Empty when the captured app is no longer owned by this account."""
    target_available: bool
    subscription_id: str
    routing_mode: EventBacklogRecipientRoutingMode
    state: EventBacklogRecipientState
    attempts: int
    capacity_deferrals: int
    pending_age_seconds: float
    """Age from acceptance at observed_at, not time spent at the latest capacity scope."""
    waiting_reason: EventBacklogRecipientWaitingReason
    """Recorded capacity takes precedence for pending recipients; receipt_processing refers only to a shared
    receipt lease."""
    receipt_url: str
    capacity_scope: EventBacklogRecipientCapacityScope | Unset = UNSET
    """Last recorded capacity scope for a currently pending recipient."""
    next_attempt_at: datetime.datetime | Unset = UNSET
    lease_until: datetime.datetime | Unset = UNSET
    """Recipient lease or shared whole-receipt lease."""
    fanout_history_url: str | Unset = UNSET
    """Present when the captured target still belongs to the account. History coverage is independently bounded."""

    def to_dict(self) -> dict[str, Any]:
        event_source = self.event_source

        event_id = self.event_id

        event_type = self.event_type

        accepted_at = self.accepted_at.isoformat()

        app_id = str(self.app_id)

        app_slug = self.app_slug

        target_available = self.target_available

        subscription_id = self.subscription_id

        routing_mode: str = self.routing_mode

        state: str = self.state

        attempts = self.attempts

        capacity_deferrals = self.capacity_deferrals

        pending_age_seconds = self.pending_age_seconds

        waiting_reason: str = self.waiting_reason

        receipt_url = self.receipt_url

        capacity_scope: str | Unset = UNSET
        if not isinstance(self.capacity_scope, Unset):
            capacity_scope = self.capacity_scope

        next_attempt_at: str | Unset = UNSET
        if not isinstance(self.next_attempt_at, Unset):
            next_attempt_at = self.next_attempt_at.isoformat()

        lease_until: str | Unset = UNSET
        if not isinstance(self.lease_until, Unset):
            lease_until = self.lease_until.isoformat()

        fanout_history_url = self.fanout_history_url

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "event_source": event_source,
                "event_id": event_id,
                "event_type": event_type,
                "accepted_at": accepted_at,
                "app_id": app_id,
                "app_slug": app_slug,
                "target_available": target_available,
                "subscription_id": subscription_id,
                "routing_mode": routing_mode,
                "state": state,
                "attempts": attempts,
                "capacity_deferrals": capacity_deferrals,
                "pending_age_seconds": pending_age_seconds,
                "waiting_reason": waiting_reason,
                "receipt_url": receipt_url,
            }
        )
        if capacity_scope is not UNSET:
            field_dict["capacity_scope"] = capacity_scope
        if next_attempt_at is not UNSET:
            field_dict["next_attempt_at"] = next_attempt_at
        if lease_until is not UNSET:
            field_dict["lease_until"] = lease_until
        if fanout_history_url is not UNSET:
            field_dict["fanout_history_url"] = fanout_history_url

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        event_source = d.pop("event_source")

        event_id = d.pop("event_id")

        event_type = d.pop("event_type")

        accepted_at = datetime.datetime.fromisoformat(d.pop("accepted_at"))

        app_id = UUID(d.pop("app_id"))

        app_slug = d.pop("app_slug")

        target_available = d.pop("target_available")

        subscription_id = d.pop("subscription_id")

        routing_mode = check_event_backlog_recipient_routing_mode(d.pop("routing_mode"))

        state = check_event_backlog_recipient_state(d.pop("state"))

        attempts = d.pop("attempts")

        capacity_deferrals = d.pop("capacity_deferrals")

        pending_age_seconds = d.pop("pending_age_seconds")

        waiting_reason = check_event_backlog_recipient_waiting_reason(d.pop("waiting_reason"))

        receipt_url = d.pop("receipt_url")

        _capacity_scope = d.pop("capacity_scope", UNSET)
        capacity_scope: EventBacklogRecipientCapacityScope | Unset
        if isinstance(_capacity_scope, Unset):
            capacity_scope = UNSET
        else:
            capacity_scope = check_event_backlog_recipient_capacity_scope(_capacity_scope)

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

        fanout_history_url = d.pop("fanout_history_url", UNSET)

        event_backlog_recipient = cls(
            event_source=event_source,
            event_id=event_id,
            event_type=event_type,
            accepted_at=accepted_at,
            app_id=app_id,
            app_slug=app_slug,
            target_available=target_available,
            subscription_id=subscription_id,
            routing_mode=routing_mode,
            state=state,
            attempts=attempts,
            capacity_deferrals=capacity_deferrals,
            pending_age_seconds=pending_age_seconds,
            waiting_reason=waiting_reason,
            receipt_url=receipt_url,
            capacity_scope=capacity_scope,
            next_attempt_at=next_attempt_at,
            lease_until=lease_until,
            fanout_history_url=fanout_history_url,
        )

        return event_backlog_recipient
