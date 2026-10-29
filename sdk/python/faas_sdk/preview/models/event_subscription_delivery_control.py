from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_circuit_breaker_response import EventCircuitBreakerResponse
    from ..models.event_subscription_capacity_command_budget import EventSubscriptionCapacityCommandBudget
    from ..models.event_subscription_capacity_command_receipt import EventSubscriptionCapacityCommandReceipt
    from ..models.event_subscription_rate_limit import EventSubscriptionRateLimit


T = TypeVar("T", bound="EventSubscriptionDeliveryControl")


@_attrs_define
class EventSubscriptionDeliveryControl:
    """Live operator controls for a captured application subscription. Pending and processing counts include publication
    and historical backfill recipients retained in the backlog.

    """

    max_pending_deliveries: int
    """Configured live-delivery ceiling; zero uses the current plan limit."""
    effective_max_pending_deliveries: int
    """Effective ceiling after applying the current plan limit."""
    live_deliveries: int
    """Current pending plus dispatching invocations with trusted delivery slots; excludes untagged legacy work."""
    subscription_id: UUID
    app_id: UUID
    paused: bool
    rate_per_second: int
    pending_recipients: int
    processing_recipients: int
    oldest_age_seconds: float
    rate_limit: EventSubscriptionRateLimit | Unset = UNSET
    """Projected token-bucket capacity at observed_at. Read-only metadata reserves no tokens. Missing rate_limit on
    older responses means unknown."""
    capacity_command_budget: EventSubscriptionCapacityCommandBudget | Unset = UNSET
    """Complete scoped advisory observation of all unexpired successful capacity-command receipts. Neither reserves
    receipt slots nor modifies retention. Known unchanged-input command repeats remain available when full; commands
    without UUIDs have no idempotent receipt evidence."""
    capacity_command_receipt: EventSubscriptionCapacityCommandReceipt | Unset = UNSET
    """Immutable acknowledgment of a successful capacity command. The surrounding control response reports current
    state; this receipt preserves the original outcome. Retained for 30 days without extension on repeat. No event
    payloads or credentials."""
    circuit_breaker: EventCircuitBreakerResponse | Unset = UNSET
    """Captured circuit policy and current breaker state for one application event subscription."""
    oldest_pending_at: datetime.datetime | Unset = UNSET
    updated_at: datetime.datetime | Unset = UNSET
    """Updated at in the event subscription delivery control: absent until an operator sets a control."""

    def to_dict(self) -> dict[str, Any]:
        max_pending_deliveries = self.max_pending_deliveries

        effective_max_pending_deliveries = self.effective_max_pending_deliveries

        live_deliveries = self.live_deliveries

        subscription_id = str(self.subscription_id)

        app_id = str(self.app_id)

        paused = self.paused

        rate_per_second = self.rate_per_second

        pending_recipients = self.pending_recipients

        processing_recipients = self.processing_recipients

        oldest_age_seconds = self.oldest_age_seconds

        rate_limit: dict[str, Any] | Unset = UNSET
        if not isinstance(self.rate_limit, Unset):
            rate_limit = self.rate_limit.to_dict()

        capacity_command_budget: dict[str, Any] | Unset = UNSET
        if not isinstance(self.capacity_command_budget, Unset):
            capacity_command_budget = self.capacity_command_budget.to_dict()

        capacity_command_receipt: dict[str, Any] | Unset = UNSET
        if not isinstance(self.capacity_command_receipt, Unset):
            capacity_command_receipt = self.capacity_command_receipt.to_dict()

        circuit_breaker: dict[str, Any] | Unset = UNSET
        if not isinstance(self.circuit_breaker, Unset):
            circuit_breaker = self.circuit_breaker.to_dict()

        oldest_pending_at: str | Unset = UNSET
        if not isinstance(self.oldest_pending_at, Unset):
            oldest_pending_at = self.oldest_pending_at.isoformat()

        updated_at: str | Unset = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "max_pending_deliveries": max_pending_deliveries,
                "effective_max_pending_deliveries": effective_max_pending_deliveries,
                "live_deliveries": live_deliveries,
                "subscription_id": subscription_id,
                "app_id": app_id,
                "paused": paused,
                "rate_per_second": rate_per_second,
                "pending_recipients": pending_recipients,
                "processing_recipients": processing_recipients,
                "oldest_age_seconds": oldest_age_seconds,
            }
        )
        if rate_limit is not UNSET:
            field_dict["rate_limit"] = rate_limit
        if capacity_command_budget is not UNSET:
            field_dict["capacity_command_budget"] = capacity_command_budget
        if capacity_command_receipt is not UNSET:
            field_dict["capacity_command_receipt"] = capacity_command_receipt
        if circuit_breaker is not UNSET:
            field_dict["circuit_breaker"] = circuit_breaker
        if oldest_pending_at is not UNSET:
            field_dict["oldest_pending_at"] = oldest_pending_at
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_circuit_breaker_response import EventCircuitBreakerResponse
        from ..models.event_subscription_capacity_command_budget import EventSubscriptionCapacityCommandBudget
        from ..models.event_subscription_capacity_command_receipt import EventSubscriptionCapacityCommandReceipt
        from ..models.event_subscription_rate_limit import EventSubscriptionRateLimit

        d = dict(src_dict)
        max_pending_deliveries = d.pop("max_pending_deliveries")

        effective_max_pending_deliveries = d.pop("effective_max_pending_deliveries")

        live_deliveries = d.pop("live_deliveries")

        subscription_id = UUID(d.pop("subscription_id"))

        app_id = UUID(d.pop("app_id"))

        paused = d.pop("paused")

        rate_per_second = d.pop("rate_per_second")

        pending_recipients = d.pop("pending_recipients")

        processing_recipients = d.pop("processing_recipients")

        oldest_age_seconds = d.pop("oldest_age_seconds")

        _rate_limit = d.pop("rate_limit", UNSET)
        rate_limit: EventSubscriptionRateLimit | Unset
        if isinstance(_rate_limit, Unset):
            rate_limit = UNSET
        else:
            rate_limit = EventSubscriptionRateLimit.from_dict(_rate_limit)

        _capacity_command_budget = d.pop("capacity_command_budget", UNSET)
        capacity_command_budget: EventSubscriptionCapacityCommandBudget | Unset
        if isinstance(_capacity_command_budget, Unset):
            capacity_command_budget = UNSET
        else:
            capacity_command_budget = EventSubscriptionCapacityCommandBudget.from_dict(_capacity_command_budget)

        _capacity_command_receipt = d.pop("capacity_command_receipt", UNSET)
        capacity_command_receipt: EventSubscriptionCapacityCommandReceipt | Unset
        if isinstance(_capacity_command_receipt, Unset):
            capacity_command_receipt = UNSET
        else:
            capacity_command_receipt = EventSubscriptionCapacityCommandReceipt.from_dict(_capacity_command_receipt)

        _circuit_breaker = d.pop("circuit_breaker", UNSET)
        circuit_breaker: EventCircuitBreakerResponse | Unset
        if isinstance(_circuit_breaker, Unset):
            circuit_breaker = UNSET
        else:
            circuit_breaker = EventCircuitBreakerResponse.from_dict(_circuit_breaker)

        _oldest_pending_at = d.pop("oldest_pending_at", UNSET)
        oldest_pending_at: datetime.datetime | Unset
        if isinstance(_oldest_pending_at, Unset):
            oldest_pending_at = UNSET
        else:
            oldest_pending_at = datetime.datetime.fromisoformat(_oldest_pending_at)

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: datetime.datetime | Unset
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = datetime.datetime.fromisoformat(_updated_at)

        event_subscription_delivery_control = cls(
            max_pending_deliveries=max_pending_deliveries,
            effective_max_pending_deliveries=effective_max_pending_deliveries,
            live_deliveries=live_deliveries,
            subscription_id=subscription_id,
            app_id=app_id,
            paused=paused,
            rate_per_second=rate_per_second,
            pending_recipients=pending_recipients,
            processing_recipients=processing_recipients,
            oldest_age_seconds=oldest_age_seconds,
            rate_limit=rate_limit,
            capacity_command_budget=capacity_command_budget,
            capacity_command_receipt=capacity_command_receipt,
            circuit_breaker=circuit_breaker,
            oldest_pending_at=oldest_pending_at,
            updated_at=updated_at,
        )

        return event_subscription_delivery_control
