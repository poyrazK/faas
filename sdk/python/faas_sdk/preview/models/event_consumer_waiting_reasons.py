from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_consumer_waiting_reasons_coverage import (
    EventConsumerWaitingReasonsCoverage,
    check_event_consumer_waiting_reasons_coverage,
)

if TYPE_CHECKING:
    from ..models.event_consumer_waiting_reasons_counts import EventConsumerWaitingReasonsCounts


T = TypeVar("T", bound="EventConsumerWaitingReasons")


@_attrs_define
class EventConsumerWaitingReasons:
    """Complete mutually exclusive effective waiting reasons across retained pending application recipients in the backlog
    snapshot. Counts sum to pending_recipients when complete, independently of recipient pagination. Missing evidence
    from older servers is unavailable, not zero.

    """

    app_id: UUID
    subscription_id: UUID
    observed_at: datetime.datetime
    window_at: datetime.datetime
    """Acceptance cutoff used by the backlog query."""
    coverage: EventConsumerWaitingReasonsCoverage
    complete: bool
    pending_recipients: int
    counts: EventConsumerWaitingReasonsCounts
    """Counts for circuit_open, circuit_probe_wait, circuit_recovery_rate_limited, subscription_paused,
    subscription_rate_limited, ordering_blocked, capacity_consumer, capacity_app, capacity_account,
    routing_in_progress, receipt_processing, retry_backoff, workflow_routing, and ready. Known reasons include zero
    counts; processing recipients are excluded."""

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        subscription_id = str(self.subscription_id)

        observed_at = self.observed_at.isoformat()

        window_at = self.window_at.isoformat()

        coverage: str = self.coverage

        complete = self.complete

        pending_recipients = self.pending_recipients

        counts = self.counts.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_id": app_id,
                "subscription_id": subscription_id,
                "observed_at": observed_at,
                "window_at": window_at,
                "coverage": coverage,
                "complete": complete,
                "pending_recipients": pending_recipients,
                "counts": counts,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_consumer_waiting_reasons_counts import EventConsumerWaitingReasonsCounts

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        subscription_id = UUID(d.pop("subscription_id"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        window_at = datetime.datetime.fromisoformat(d.pop("window_at"))

        coverage = check_event_consumer_waiting_reasons_coverage(d.pop("coverage"))

        complete = d.pop("complete")

        pending_recipients = d.pop("pending_recipients")

        counts = EventConsumerWaitingReasonsCounts.from_dict(d.pop("counts"))

        event_consumer_waiting_reasons = cls(
            app_id=app_id,
            subscription_id=subscription_id,
            observed_at=observed_at,
            window_at=window_at,
            coverage=coverage,
            complete=complete,
            pending_recipients=pending_recipients,
            counts=counts,
        )

        return event_consumer_waiting_reasons
