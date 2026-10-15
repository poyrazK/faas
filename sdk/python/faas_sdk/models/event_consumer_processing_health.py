from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_consumer_processing_health_coverage import (
    EventConsumerProcessingHealthCoverage,
    check_event_consumer_processing_health_coverage,
)
from ..models.event_consumer_processing_health_renewal_failure_coverage import (
    EventConsumerProcessingHealthRenewalFailureCoverage,
    check_event_consumer_processing_health_renewal_failure_coverage,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EventConsumerProcessingHealth")


@_attrs_define
class EventConsumerProcessingHealth:
    """Current retained deferred invocation counts plus issue-to-completion latency of retained completed claims; pruned
    history and rejected renewals are excluded.

    """

    app_id: UUID
    """Application owning this processing summary."""
    subscription_id: UUID
    """Consumer subscription selected for the retained processing aggregate."""
    observed_at: datetime.datetime
    """Database snapshot clock for processing gauges."""
    window_start: datetime.datetime
    """Start of the retained acknowledgment completion window."""
    coverage: EventConsumerProcessingHealthCoverage
    """Coverage is limited to currently retained opted-in invocations."""
    complete: bool
    """All matching retained invocations were aggregated without sampling; this is not historical completeness."""
    waiting: int
    """Deferred invocations currently pending dispatch."""
    delivering: int
    """Live deferred dispatches not yet handed off."""
    processing: int
    """Unexpired matching leases handed off after HTTP 202."""
    expired: int
    """Expired dispatching leases before recovery sweeps."""
    completed: int
    """Retained opted-in invocations completed by callback or ordinary success."""
    dead_lettered: int
    """Retained deferred invocations in dead letters."""
    failed: int
    """Retained deferred invocations permanently failed."""
    other: int
    """Other terminal states including cancellation and supersession and work-policy expiry."""
    near_expiry: int
    """Active handed-off leases in their final third; the risk horizon is capped by near_expiry_seconds."""
    near_expiry_seconds: int
    """Maximum lease risk horizon in seconds; each claim uses the smaller of this cap and one third of its lease
    duration."""
    oldest_processing_age_seconds: float
    """Age since oldest active processing token issue; zero when absent."""
    acknowledgment_samples: int
    """Retained matching explicit completions whose completion time falls inside the window."""
    acknowledgment_latency_max_seconds: float
    """Maximum token-issue-to-completion seconds among acknowledgment samples; zero requires inspecting sample
    count."""
    renewal_failure_coverage: EventConsumerProcessingHealthRenewalFailureCoverage
    """Rejected renewal calls are not durably recorded; no failure count is inferred."""
    oldest_processing_offered_at: datetime.datetime | Unset = UNSET
    """Oldest token issue time among live handed-off claims; includes initial dispatch time."""

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        subscription_id = str(self.subscription_id)

        observed_at = self.observed_at.isoformat()

        window_start = self.window_start.isoformat()

        coverage: str = self.coverage

        complete = self.complete

        waiting = self.waiting

        delivering = self.delivering

        processing = self.processing

        expired = self.expired

        completed = self.completed

        dead_lettered = self.dead_lettered

        failed = self.failed

        other = self.other

        near_expiry = self.near_expiry

        near_expiry_seconds = self.near_expiry_seconds

        oldest_processing_age_seconds = self.oldest_processing_age_seconds

        acknowledgment_samples = self.acknowledgment_samples

        acknowledgment_latency_max_seconds = self.acknowledgment_latency_max_seconds

        renewal_failure_coverage: str = self.renewal_failure_coverage

        oldest_processing_offered_at: str | Unset = UNSET
        if not isinstance(self.oldest_processing_offered_at, Unset):
            oldest_processing_offered_at = self.oldest_processing_offered_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_id": app_id,
                "subscription_id": subscription_id,
                "observed_at": observed_at,
                "window_start": window_start,
                "coverage": coverage,
                "complete": complete,
                "waiting": waiting,
                "delivering": delivering,
                "processing": processing,
                "expired": expired,
                "completed": completed,
                "dead_lettered": dead_lettered,
                "failed": failed,
                "other": other,
                "near_expiry": near_expiry,
                "near_expiry_seconds": near_expiry_seconds,
                "oldest_processing_age_seconds": oldest_processing_age_seconds,
                "acknowledgment_samples": acknowledgment_samples,
                "acknowledgment_latency_max_seconds": acknowledgment_latency_max_seconds,
                "renewal_failure_coverage": renewal_failure_coverage,
            }
        )
        if oldest_processing_offered_at is not UNSET:
            field_dict["oldest_processing_offered_at"] = oldest_processing_offered_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        subscription_id = UUID(d.pop("subscription_id"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        window_start = datetime.datetime.fromisoformat(d.pop("window_start"))

        coverage = check_event_consumer_processing_health_coverage(d.pop("coverage"))

        complete = d.pop("complete")

        waiting = d.pop("waiting")

        delivering = d.pop("delivering")

        processing = d.pop("processing")

        expired = d.pop("expired")

        completed = d.pop("completed")

        dead_lettered = d.pop("dead_lettered")

        failed = d.pop("failed")

        other = d.pop("other")

        near_expiry = d.pop("near_expiry")

        near_expiry_seconds = d.pop("near_expiry_seconds")

        oldest_processing_age_seconds = d.pop("oldest_processing_age_seconds")

        acknowledgment_samples = d.pop("acknowledgment_samples")

        acknowledgment_latency_max_seconds = d.pop("acknowledgment_latency_max_seconds")

        renewal_failure_coverage = check_event_consumer_processing_health_renewal_failure_coverage(
            d.pop("renewal_failure_coverage")
        )

        _oldest_processing_offered_at = d.pop("oldest_processing_offered_at", UNSET)
        oldest_processing_offered_at: datetime.datetime | Unset
        if isinstance(_oldest_processing_offered_at, Unset):
            oldest_processing_offered_at = UNSET
        else:
            oldest_processing_offered_at = datetime.datetime.fromisoformat(_oldest_processing_offered_at)

        event_consumer_processing_health = cls(
            app_id=app_id,
            subscription_id=subscription_id,
            observed_at=observed_at,
            window_start=window_start,
            coverage=coverage,
            complete=complete,
            waiting=waiting,
            delivering=delivering,
            processing=processing,
            expired=expired,
            completed=completed,
            dead_lettered=dead_lettered,
            failed=failed,
            other=other,
            near_expiry=near_expiry,
            near_expiry_seconds=near_expiry_seconds,
            oldest_processing_age_seconds=oldest_processing_age_seconds,
            acknowledgment_samples=acknowledgment_samples,
            acknowledgment_latency_max_seconds=acknowledgment_latency_max_seconds,
            renewal_failure_coverage=renewal_failure_coverage,
            oldest_processing_offered_at=oldest_processing_offered_at,
        )

        return event_consumer_processing_health
