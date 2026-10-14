from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_subscription_capacity_command_receipt_outcome import (
    EventSubscriptionCapacityCommandReceiptOutcome,
    check_event_subscription_capacity_command_receipt_outcome,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_subscription_capacity_guard import EventSubscriptionCapacityGuard


T = TypeVar("T", bound="EventSubscriptionCapacityCommandReceipt")


@_attrs_define
class EventSubscriptionCapacityCommandReceipt:
    """Immutable acknowledgment of a successful capacity command. The surrounding control response reports current state;
    this receipt preserves the original outcome. Retained for 30 days without extension on repeat. No event payloads or
    credentials.

    """

    command_id: UUID
    app_id: UUID
    subscription_id: UUID
    requested_max_pending_deliveries: int
    previous_max_pending_deliveries: int
    plan_max_pending_deliveries: int
    effective_max_pending_deliveries: int
    outcome: EventSubscriptionCapacityCommandReceiptOutcome
    """Whether the original successful command changed the saved setting; both consume a receipt slot."""
    recorded_at: datetime.datetime
    retain_until: datetime.datetime
    expected: EventSubscriptionCapacityGuard | Unset = UNSET
    """Value preconditions checked atomically before changing subscription capacity. Both fields are required.
    Checks saved configuration and the current plan allowance; live usage is not reserved."""

    def to_dict(self) -> dict[str, Any]:
        command_id = str(self.command_id)

        app_id = str(self.app_id)

        subscription_id = str(self.subscription_id)

        requested_max_pending_deliveries = self.requested_max_pending_deliveries

        previous_max_pending_deliveries = self.previous_max_pending_deliveries

        plan_max_pending_deliveries = self.plan_max_pending_deliveries

        effective_max_pending_deliveries = self.effective_max_pending_deliveries

        outcome: str = self.outcome

        recorded_at = self.recorded_at.isoformat()

        retain_until = self.retain_until.isoformat()

        expected: dict[str, Any] | Unset = UNSET
        if not isinstance(self.expected, Unset):
            expected = self.expected.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "command_id": command_id,
                "app_id": app_id,
                "subscription_id": subscription_id,
                "requested_max_pending_deliveries": requested_max_pending_deliveries,
                "previous_max_pending_deliveries": previous_max_pending_deliveries,
                "plan_max_pending_deliveries": plan_max_pending_deliveries,
                "effective_max_pending_deliveries": effective_max_pending_deliveries,
                "outcome": outcome,
                "recorded_at": recorded_at,
                "retain_until": retain_until,
            }
        )
        if expected is not UNSET:
            field_dict["expected"] = expected

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_subscription_capacity_guard import EventSubscriptionCapacityGuard

        d = dict(src_dict)
        command_id = UUID(d.pop("command_id"))

        app_id = UUID(d.pop("app_id"))

        subscription_id = UUID(d.pop("subscription_id"))

        requested_max_pending_deliveries = d.pop("requested_max_pending_deliveries")

        previous_max_pending_deliveries = d.pop("previous_max_pending_deliveries")

        plan_max_pending_deliveries = d.pop("plan_max_pending_deliveries")

        effective_max_pending_deliveries = d.pop("effective_max_pending_deliveries")

        outcome = check_event_subscription_capacity_command_receipt_outcome(d.pop("outcome"))

        recorded_at = datetime.datetime.fromisoformat(d.pop("recorded_at"))

        retain_until = datetime.datetime.fromisoformat(d.pop("retain_until"))

        _expected = d.pop("expected", UNSET)
        expected: EventSubscriptionCapacityGuard | Unset
        if isinstance(_expected, Unset):
            expected = UNSET
        else:
            expected = EventSubscriptionCapacityGuard.from_dict(_expected)

        event_subscription_capacity_command_receipt = cls(
            command_id=command_id,
            app_id=app_id,
            subscription_id=subscription_id,
            requested_max_pending_deliveries=requested_max_pending_deliveries,
            previous_max_pending_deliveries=previous_max_pending_deliveries,
            plan_max_pending_deliveries=plan_max_pending_deliveries,
            effective_max_pending_deliveries=effective_max_pending_deliveries,
            outcome=outcome,
            recorded_at=recorded_at,
            retain_until=retain_until,
            expected=expected,
        )

        return event_subscription_capacity_command_receipt
