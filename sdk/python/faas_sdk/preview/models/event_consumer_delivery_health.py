from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_consumer_capacity_pressure import EventConsumerCapacityPressure
    from ..models.event_consumer_capacity_waits import EventConsumerCapacityWaits
    from ..models.event_consumer_execution_health import EventConsumerExecutionHealth
    from ..models.event_consumer_health import EventConsumerHealth
    from ..models.event_consumer_pending_sample import EventConsumerPendingSample
    from ..models.event_consumer_processing_health import EventConsumerProcessingHealth
    from ..models.event_consumer_waiting_reasons import EventConsumerWaitingReasons


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
    Attempts and successful completion latency use an immutable, per-subscription outcome ledger with 30-day
    retention. Window evidence survives source deletion and is independent of retained execution-state truncation.
    Counts are bounded observations, include backfills, and are not unique event counts. Version filtering and
    routing failures are excluded. History completeness is established only for fully covered windows of newly
    instrumented subscriptions; legacy baselines remain unknown and evidence loss is partial."""
    pending_sample: EventConsumerPendingSample
    """Up to 50 oldest pending application recipients, ordered by acceptance time with stable outbox and
    subscription ties. Waiting reason counts cover only these recipients."""
    processing: EventConsumerProcessingHealth | Unset = UNSET
    """Current retained deferred invocation gauges, retained completion latency and optional durable full-minute
    renewal metrics with independent coverage."""
    capacity_pressure: EventConsumerCapacityPressure | Unset = UNSET
    """Read-only current live slot usage and effective capacity waiting reasons from one owned subscription
    snapshot. Independent of the other delivery-health reads and history window. Recorded waits may remain after
    capacity drains. Age is since event acceptance, not continuous capacity-wait duration. No capacity reservation
    or delivery-order guarantee."""
    waiting_reasons: EventConsumerWaitingReasons | Unset = UNSET
    """Complete mutually exclusive effective waiting reasons across retained pending application recipients in the
    backlog snapshot. Counts sum to pending_recipients when complete, independently of recipient pagination. Missing
    evidence from older servers is unavailable, not zero."""
    capacity_waits: EventConsumerCapacityWaits | Unset = UNSET
    """Complete counts of effective capacity waiting reasons among retained pending application recipients in one
    backlog snapshot. Independent of recipient pagination. Missing or incomplete evidence cannot clear capacity
    alerts."""

    def to_dict(self) -> dict[str, Any]:
        started_at = self.started_at.isoformat()

        observed_at = self.observed_at.isoformat()

        routing = self.routing.to_dict()

        execution = self.execution.to_dict()

        pending_sample = self.pending_sample.to_dict()

        processing: dict[str, Any] | Unset = UNSET
        if not isinstance(self.processing, Unset):
            processing = self.processing.to_dict()

        capacity_pressure: dict[str, Any] | Unset = UNSET
        if not isinstance(self.capacity_pressure, Unset):
            capacity_pressure = self.capacity_pressure.to_dict()

        waiting_reasons: dict[str, Any] | Unset = UNSET
        if not isinstance(self.waiting_reasons, Unset):
            waiting_reasons = self.waiting_reasons.to_dict()

        capacity_waits: dict[str, Any] | Unset = UNSET
        if not isinstance(self.capacity_waits, Unset):
            capacity_waits = self.capacity_waits.to_dict()

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
        if processing is not UNSET:
            field_dict["processing"] = processing
        if capacity_pressure is not UNSET:
            field_dict["capacity_pressure"] = capacity_pressure
        if waiting_reasons is not UNSET:
            field_dict["waiting_reasons"] = waiting_reasons
        if capacity_waits is not UNSET:
            field_dict["capacity_waits"] = capacity_waits

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_consumer_capacity_pressure import EventConsumerCapacityPressure
        from ..models.event_consumer_capacity_waits import EventConsumerCapacityWaits
        from ..models.event_consumer_execution_health import EventConsumerExecutionHealth
        from ..models.event_consumer_health import EventConsumerHealth
        from ..models.event_consumer_pending_sample import EventConsumerPendingSample
        from ..models.event_consumer_processing_health import EventConsumerProcessingHealth
        from ..models.event_consumer_waiting_reasons import EventConsumerWaitingReasons

        d = dict(src_dict)
        started_at = datetime.datetime.fromisoformat(d.pop("started_at"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        routing = EventConsumerHealth.from_dict(d.pop("routing"))

        execution = EventConsumerExecutionHealth.from_dict(d.pop("execution"))

        pending_sample = EventConsumerPendingSample.from_dict(d.pop("pending_sample"))

        _processing = d.pop("processing", UNSET)
        processing: EventConsumerProcessingHealth | Unset
        if isinstance(_processing, Unset):
            processing = UNSET
        else:
            processing = EventConsumerProcessingHealth.from_dict(_processing)

        _capacity_pressure = d.pop("capacity_pressure", UNSET)
        capacity_pressure: EventConsumerCapacityPressure | Unset
        if isinstance(_capacity_pressure, Unset):
            capacity_pressure = UNSET
        else:
            capacity_pressure = EventConsumerCapacityPressure.from_dict(_capacity_pressure)

        _waiting_reasons = d.pop("waiting_reasons", UNSET)
        waiting_reasons: EventConsumerWaitingReasons | Unset
        if isinstance(_waiting_reasons, Unset):
            waiting_reasons = UNSET
        else:
            waiting_reasons = EventConsumerWaitingReasons.from_dict(_waiting_reasons)

        _capacity_waits = d.pop("capacity_waits", UNSET)
        capacity_waits: EventConsumerCapacityWaits | Unset
        if isinstance(_capacity_waits, Unset):
            capacity_waits = UNSET
        else:
            capacity_waits = EventConsumerCapacityWaits.from_dict(_capacity_waits)

        event_consumer_delivery_health = cls(
            started_at=started_at,
            observed_at=observed_at,
            routing=routing,
            execution=execution,
            pending_sample=pending_sample,
            processing=processing,
            capacity_pressure=capacity_pressure,
            waiting_reasons=waiting_reasons,
            capacity_waits=capacity_waits,
        )

        return event_consumer_delivery_health
