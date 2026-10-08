from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_consumer_health_coverage import EventConsumerHealthCoverage, check_event_consumer_health_coverage
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_circuit_breaker_response import EventCircuitBreakerResponse


T = TypeVar("T", bound="EventConsumerHealth")


@_attrs_define
class EventConsumerHealth:
    """Current subscription backlog and bounded recorded routing outcomes. History compaction can remove outcomes; rates
    then represent retained samples. Latency runs from event acceptance to successful routing admission.

    """

    subscription_id: UUID
    app_id: UUID
    paused: bool
    rate_per_second: int
    pending_recipients: int
    processing_recipients: int
    oldest_age_seconds: float
    observed_at: datetime.datetime
    window_start: datetime.datetime
    coverage: EventConsumerHealthCoverage
    paused_seconds: float
    retry_scheduled: int
    successful_routes: int
    terminal_failures: int
    retry_rate_per_second: float
    terminal_failure_pct: float
    routing_latency_p95_seconds: float
    drain_rate_per_second: float
    history_compacted: bool
    expired_deliveries: int | Unset = UNSET
    """Recorded delivery_expired outcomes in the observation window; included in terminal_failures."""
    circuit_breaker: EventCircuitBreakerResponse | Unset = UNSET
    oldest_pending_at: datetime.datetime | Unset = UNSET
    updated_at: datetime.datetime | Unset = UNSET
    """Absent until an operator sets a control."""

    def to_dict(self) -> dict[str, Any]:
        subscription_id = str(self.subscription_id)

        app_id = str(self.app_id)

        paused = self.paused

        rate_per_second = self.rate_per_second

        pending_recipients = self.pending_recipients

        processing_recipients = self.processing_recipients

        oldest_age_seconds = self.oldest_age_seconds

        observed_at = self.observed_at.isoformat()

        window_start = self.window_start.isoformat()

        coverage: str = self.coverage

        paused_seconds = self.paused_seconds

        retry_scheduled = self.retry_scheduled

        successful_routes = self.successful_routes

        terminal_failures = self.terminal_failures

        retry_rate_per_second = self.retry_rate_per_second

        terminal_failure_pct = self.terminal_failure_pct

        routing_latency_p95_seconds = self.routing_latency_p95_seconds

        drain_rate_per_second = self.drain_rate_per_second

        history_compacted = self.history_compacted

        expired_deliveries = self.expired_deliveries

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
                "subscription_id": subscription_id,
                "app_id": app_id,
                "paused": paused,
                "rate_per_second": rate_per_second,
                "pending_recipients": pending_recipients,
                "processing_recipients": processing_recipients,
                "oldest_age_seconds": oldest_age_seconds,
                "observed_at": observed_at,
                "window_start": window_start,
                "coverage": coverage,
                "paused_seconds": paused_seconds,
                "retry_scheduled": retry_scheduled,
                "successful_routes": successful_routes,
                "terminal_failures": terminal_failures,
                "retry_rate_per_second": retry_rate_per_second,
                "terminal_failure_pct": terminal_failure_pct,
                "routing_latency_p95_seconds": routing_latency_p95_seconds,
                "drain_rate_per_second": drain_rate_per_second,
                "history_compacted": history_compacted,
            }
        )
        if expired_deliveries is not UNSET:
            field_dict["expired_deliveries"] = expired_deliveries
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

        d = dict(src_dict)
        subscription_id = UUID(d.pop("subscription_id"))

        app_id = UUID(d.pop("app_id"))

        paused = d.pop("paused")

        rate_per_second = d.pop("rate_per_second")

        pending_recipients = d.pop("pending_recipients")

        processing_recipients = d.pop("processing_recipients")

        oldest_age_seconds = d.pop("oldest_age_seconds")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        window_start = datetime.datetime.fromisoformat(d.pop("window_start"))

        coverage = check_event_consumer_health_coverage(d.pop("coverage"))

        paused_seconds = d.pop("paused_seconds")

        retry_scheduled = d.pop("retry_scheduled")

        successful_routes = d.pop("successful_routes")

        terminal_failures = d.pop("terminal_failures")

        retry_rate_per_second = d.pop("retry_rate_per_second")

        terminal_failure_pct = d.pop("terminal_failure_pct")

        routing_latency_p95_seconds = d.pop("routing_latency_p95_seconds")

        drain_rate_per_second = d.pop("drain_rate_per_second")

        history_compacted = d.pop("history_compacted")

        expired_deliveries = d.pop("expired_deliveries", UNSET)

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

        event_consumer_health = cls(
            subscription_id=subscription_id,
            app_id=app_id,
            paused=paused,
            rate_per_second=rate_per_second,
            pending_recipients=pending_recipients,
            processing_recipients=processing_recipients,
            oldest_age_seconds=oldest_age_seconds,
            observed_at=observed_at,
            window_start=window_start,
            coverage=coverage,
            paused_seconds=paused_seconds,
            retry_scheduled=retry_scheduled,
            successful_routes=successful_routes,
            terminal_failures=terminal_failures,
            retry_rate_per_second=retry_rate_per_second,
            terminal_failure_pct=terminal_failure_pct,
            routing_latency_p95_seconds=routing_latency_p95_seconds,
            drain_rate_per_second=drain_rate_per_second,
            history_compacted=history_compacted,
            expired_deliveries=expired_deliveries,
            circuit_breaker=circuit_breaker,
            oldest_pending_at=oldest_pending_at,
            updated_at=updated_at,
        )

        return event_consumer_health
