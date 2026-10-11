from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.event_consumer_execution_health import EventConsumerExecutionHealth
    from ..models.event_consumer_health import EventConsumerHealth
    from ..models.event_consumer_pending_sample import EventConsumerPendingSample


T = TypeVar("T", bound="EventConsumerDeliveryHealth")


@_attrs_define
class EventConsumerDeliveryHealth:
    """Independent live routing, retained execution, and oldest-first pending reads. No overall healthy verdict is inferred
    from incomplete coverage.

    """

    started_at: datetime.datetime
    observed_at: datetime.datetime
    routing: EventConsumerHealth
    """Current subscription backlog and bounded recorded routing outcomes. History compaction can remove outcomes;
    rates then represent retained samples. Latency runs from event acceptance to successful routing admission."""
    execution: EventConsumerExecutionHealth
    """Current retained execution states for admitted application event deliveries and retained handler replays.
    Attempts and successful completion latency use the requested window. Counts are bounded observations, include
    backfills, and are not unique event counts. Version filtering and routing failures are excluded. Historical
    coverage is always incomplete."""
    pending_sample: EventConsumerPendingSample
    """Up to 50 oldest pending application recipients, ordered by acceptance time with stable outbox and
    subscription ties. Waiting reason counts cover only these recipients."""

    def to_dict(self) -> dict[str, Any]:
        started_at = self.started_at.isoformat()

        observed_at = self.observed_at.isoformat()

        routing = self.routing.to_dict()

        execution = self.execution.to_dict()

        pending_sample = self.pending_sample.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "started_at": started_at,
                "observed_at": observed_at,
                "routing": routing,
                "execution": execution,
                "pending_sample": pending_sample,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_consumer_execution_health import EventConsumerExecutionHealth
        from ..models.event_consumer_health import EventConsumerHealth
        from ..models.event_consumer_pending_sample import EventConsumerPendingSample

        d = dict(src_dict)
        started_at = datetime.datetime.fromisoformat(d.pop("started_at"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        routing = EventConsumerHealth.from_dict(d.pop("routing"))

        execution = EventConsumerExecutionHealth.from_dict(d.pop("execution"))

        pending_sample = EventConsumerPendingSample.from_dict(d.pop("pending_sample"))

        event_consumer_delivery_health = cls(
            started_at=started_at,
            observed_at=observed_at,
            routing=routing,
            execution=execution,
            pending_sample=pending_sample,
        )

        return event_consumer_delivery_health
