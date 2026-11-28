from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_subscription_rate_command_receipt_outcome import (
    EventSubscriptionRateCommandReceiptOutcome,
    check_event_subscription_rate_command_receipt_outcome,
)

if TYPE_CHECKING:
    from ..models.event_subscription_rate_limit import EventSubscriptionRateLimit


T = TypeVar("T", bound="EventSubscriptionRateCommandReceipt")


@_attrs_define
class EventSubscriptionRateCommandReceipt:
    """Historical idempotent command outcome; rejected commands preserve configuration. Look up after an uncertain mutation
    before submitting a new UUID.

    """

    command_id: UUID
    app_id: UUID
    subscription_id: UUID
    sequence: int
    outcome: EventSubscriptionRateCommandReceiptOutcome
    result: EventSubscriptionRateLimit
    """Projected token-bucket capacity at observed_at. Read-only metadata reserves no tokens. Missing rate_limit on
    older responses means unknown."""
    recorded_at: datetime.datetime
    retain_until: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        command_id = str(self.command_id)

        app_id = str(self.app_id)

        subscription_id = str(self.subscription_id)

        sequence = self.sequence

        outcome: str = self.outcome

        result = self.result.to_dict()

        recorded_at = self.recorded_at.isoformat()

        retain_until = self.retain_until.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "command_id": command_id,
                "app_id": app_id,
                "subscription_id": subscription_id,
                "sequence": sequence,
                "outcome": outcome,
                "result": result,
                "recorded_at": recorded_at,
                "retain_until": retain_until,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_subscription_rate_limit import EventSubscriptionRateLimit

        d = dict(src_dict)
        command_id = UUID(d.pop("command_id"))

        app_id = UUID(d.pop("app_id"))

        subscription_id = UUID(d.pop("subscription_id"))

        sequence = d.pop("sequence")

        outcome = check_event_subscription_rate_command_receipt_outcome(d.pop("outcome"))

        result = EventSubscriptionRateLimit.from_dict(d.pop("result"))

        recorded_at = datetime.datetime.fromisoformat(d.pop("recorded_at"))

        retain_until = datetime.datetime.fromisoformat(d.pop("retain_until"))

        event_subscription_rate_command_receipt = cls(
            command_id=command_id,
            app_id=app_id,
            subscription_id=subscription_id,
            sequence=sequence,
            outcome=outcome,
            result=result,
            recorded_at=recorded_at,
            retain_until=retain_until,
        )

        return event_subscription_rate_command_receipt
