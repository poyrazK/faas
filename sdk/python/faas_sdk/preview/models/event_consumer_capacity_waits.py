from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_consumer_capacity_waits_coverage import (
    EventConsumerCapacityWaitsCoverage,
    check_event_consumer_capacity_waits_coverage,
)

T = TypeVar("T", bound="EventConsumerCapacityWaits")


@_attrs_define
class EventConsumerCapacityWaits:
    """Complete counts of effective capacity waiting reasons among retained pending application recipients in one backlog
    snapshot. Independent of recipient pagination. Missing or incomplete evidence cannot clear capacity alerts.

    """

    app_id: UUID
    subscription_id: UUID
    observed_at: datetime.datetime
    coverage: EventConsumerCapacityWaitsCoverage
    complete: bool
    pending_recipients: int
    consumer: int
    app: int
    account: int

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        subscription_id = str(self.subscription_id)

        observed_at = self.observed_at.isoformat()

        coverage: str = self.coverage

        complete = self.complete

        pending_recipients = self.pending_recipients

        consumer = self.consumer

        app = self.app

        account = self.account

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_id": app_id,
                "subscription_id": subscription_id,
                "observed_at": observed_at,
                "coverage": coverage,
                "complete": complete,
                "pending_recipients": pending_recipients,
                "consumer": consumer,
                "app": app,
                "account": account,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        subscription_id = UUID(d.pop("subscription_id"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        coverage = check_event_consumer_capacity_waits_coverage(d.pop("coverage"))

        complete = d.pop("complete")

        pending_recipients = d.pop("pending_recipients")

        consumer = d.pop("consumer")

        app = d.pop("app")

        account = d.pop("account")

        event_consumer_capacity_waits = cls(
            app_id=app_id,
            subscription_id=subscription_id,
            observed_at=observed_at,
            coverage=coverage,
            complete=complete,
            pending_recipients=pending_recipients,
            consumer=consumer,
            app=app,
            account=account,
        )

        return event_consumer_capacity_waits
