from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_subscription_capacity_command_budget_limit import (
    EventSubscriptionCapacityCommandBudgetLimit,
    check_event_subscription_capacity_command_budget_limit,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EventSubscriptionCapacityCommandBudget")


@_attrs_define
class EventSubscriptionCapacityCommandBudget:
    """Complete scoped advisory observation of all unexpired successful capacity-command receipts. Neither reserves receipt
    slots nor modifies retention. Known unchanged-input command repeats remain available when full; commands without
    UUIDs have no idempotent receipt evidence.

    """

    app_id: UUID
    """Owned application identifier. Context: Event Subscription Capacity Command Budget."""
    subscription_id: UUID
    """Owned subscription or retained control identifier. Context: Event Subscription Capacity Command Budget."""
    observed_at: datetime.datetime
    """Logical observation time used for receipt expiry."""
    complete: bool
    """All matching unexpired receipts were counted rather than sampled from a history page."""
    retained_receipts: int
    """Successful applied and unchanged command receipts whose retention deadline is after observed_at."""
    limit: EventSubscriptionCapacityCommandBudgetLimit
    """Maximum retained receipts per subscription."""
    remaining_slots: int
    """Limit minus retained receipts at the observation. Concurrent writes may consume slots."""
    full: bool
    """Whether no slots for new command UUIDs were available at the observation."""
    advice: str
    """Advisory guidance without a reservation or write authorization guarantee."""
    earliest_expiry_at: datetime.datetime | Unset = UNSET
    """Earliest retained receipt deadline that could free a slot. Omitted when no receipts remain. Logical expiry
    frees quota without waiting for physical pruning."""

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        subscription_id = str(self.subscription_id)

        observed_at = self.observed_at.isoformat()

        complete = self.complete

        retained_receipts = self.retained_receipts

        limit: int = self.limit

        remaining_slots = self.remaining_slots

        full = self.full

        advice = self.advice

        earliest_expiry_at: str | Unset = UNSET
        if not isinstance(self.earliest_expiry_at, Unset):
            earliest_expiry_at = self.earliest_expiry_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_id": app_id,
                "subscription_id": subscription_id,
                "observed_at": observed_at,
                "complete": complete,
                "retained_receipts": retained_receipts,
                "limit": limit,
                "remaining_slots": remaining_slots,
                "full": full,
                "advice": advice,
            }
        )
        if earliest_expiry_at is not UNSET:
            field_dict["earliest_expiry_at"] = earliest_expiry_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        subscription_id = UUID(d.pop("subscription_id"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        complete = d.pop("complete")

        retained_receipts = d.pop("retained_receipts")

        limit = check_event_subscription_capacity_command_budget_limit(d.pop("limit"))

        remaining_slots = d.pop("remaining_slots")

        full = d.pop("full")

        advice = d.pop("advice")

        _earliest_expiry_at = d.pop("earliest_expiry_at", UNSET)
        earliest_expiry_at: datetime.datetime | Unset
        if isinstance(_earliest_expiry_at, Unset):
            earliest_expiry_at = UNSET
        else:
            earliest_expiry_at = datetime.datetime.fromisoformat(_earliest_expiry_at)

        event_subscription_capacity_command_budget = cls(
            app_id=app_id,
            subscription_id=subscription_id,
            observed_at=observed_at,
            complete=complete,
            retained_receipts=retained_receipts,
            limit=limit,
            remaining_slots=remaining_slots,
            full=full,
            advice=advice,
            earliest_expiry_at=earliest_expiry_at,
        )

        return event_subscription_capacity_command_budget
