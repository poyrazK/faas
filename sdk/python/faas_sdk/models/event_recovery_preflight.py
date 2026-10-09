from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_recovery_preflight_state import EventRecoveryPreflightState, check_event_recovery_preflight_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_recovery_preflight_capacity_scopes import EventRecoveryPreflightCapacityScopes
    from ..models.event_recovery_preflight_item import EventRecoveryPreflightItem
    from ..models.event_recovery_preflight_reason_counts import EventRecoveryPreflightReasonCounts


T = TypeVar("T", bound="EventRecoveryPreflight")


@_attrs_define
class EventRecoveryPreflight:
    """Read-only pending-item eligibility, current capacity and optimistic drain estimate for a recovery job."""

    job_id: UUID
    observed_at: datetime.datetime
    state: EventRecoveryPreflightState
    active: bool
    """Running or paused and not expired at the observation time."""
    pending_count: int
    eligible_count: int
    waiting_count: int
    likely_skipped_count: int
    unknown_count: int
    reason_counts: EventRecoveryPreflightReasonCounts
    capacity_scopes: EventRecoveryPreflightCapacityScopes
    rate_per_second: int
    remaining_lifetime_seconds: float
    minimum_drain_seconds: float
    """Optimistic delay until the last pending item can spend an admission permit, respecting the current fixed-
    window budget and schedule. Does not include handler execution or future waits."""
    earliest_drain_at: datetime.datetime
    fits_before_expiry: bool
    """Active job and optimistic earliest drain strictly precedes expiry; true is not a guarantee of completion."""
    assumes_immediate_resume: bool
    sample: list[EventRecoveryPreflightItem]
    receipt_retention_warning_count: int | Unset = UNSET
    """Pending items with unheld receipt deadlines at or before the later of 24 hours from observation and
    optimistic drain."""
    receipt_retention_held_count: int | Unset = UNSET
    """Pending items currently pinned by a backfill."""
    earliest_unheld_retain_until: datetime.datetime | Unset = UNSET
    minimum_drain_crosses_receipt_retention: bool | Unset = UNSET
    """Rate-only optimistic drain reaches or exceeds the earliest currently unheld receipt boundary; false is not a
    retention guarantee."""

    def to_dict(self) -> dict[str, Any]:
        job_id = str(self.job_id)

        observed_at = self.observed_at.isoformat()

        state: str = self.state

        active = self.active

        pending_count = self.pending_count

        eligible_count = self.eligible_count

        waiting_count = self.waiting_count

        likely_skipped_count = self.likely_skipped_count

        unknown_count = self.unknown_count

        reason_counts = self.reason_counts.to_dict()

        capacity_scopes = self.capacity_scopes.to_dict()

        rate_per_second = self.rate_per_second

        remaining_lifetime_seconds = self.remaining_lifetime_seconds

        minimum_drain_seconds = self.minimum_drain_seconds

        earliest_drain_at = self.earliest_drain_at.isoformat()

        fits_before_expiry = self.fits_before_expiry

        assumes_immediate_resume = self.assumes_immediate_resume

        sample = []
        for sample_item_data in self.sample:
            sample_item = sample_item_data.to_dict()
            sample.append(sample_item)

        receipt_retention_warning_count = self.receipt_retention_warning_count

        receipt_retention_held_count = self.receipt_retention_held_count

        earliest_unheld_retain_until: str | Unset = UNSET
        if not isinstance(self.earliest_unheld_retain_until, Unset):
            earliest_unheld_retain_until = self.earliest_unheld_retain_until.isoformat()

        minimum_drain_crosses_receipt_retention = self.minimum_drain_crosses_receipt_retention

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "job_id": job_id,
                "observed_at": observed_at,
                "state": state,
                "active": active,
                "pending_count": pending_count,
                "eligible_count": eligible_count,
                "waiting_count": waiting_count,
                "likely_skipped_count": likely_skipped_count,
                "unknown_count": unknown_count,
                "reason_counts": reason_counts,
                "capacity_scopes": capacity_scopes,
                "rate_per_second": rate_per_second,
                "remaining_lifetime_seconds": remaining_lifetime_seconds,
                "minimum_drain_seconds": minimum_drain_seconds,
                "earliest_drain_at": earliest_drain_at,
                "fits_before_expiry": fits_before_expiry,
                "assumes_immediate_resume": assumes_immediate_resume,
                "sample": sample,
            }
        )
        if receipt_retention_warning_count is not UNSET:
            field_dict["receipt_retention_warning_count"] = receipt_retention_warning_count
        if receipt_retention_held_count is not UNSET:
            field_dict["receipt_retention_held_count"] = receipt_retention_held_count
        if earliest_unheld_retain_until is not UNSET:
            field_dict["earliest_unheld_retain_until"] = earliest_unheld_retain_until
        if minimum_drain_crosses_receipt_retention is not UNSET:
            field_dict["minimum_drain_crosses_receipt_retention"] = minimum_drain_crosses_receipt_retention

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_recovery_preflight_capacity_scopes import EventRecoveryPreflightCapacityScopes
        from ..models.event_recovery_preflight_item import EventRecoveryPreflightItem
        from ..models.event_recovery_preflight_reason_counts import EventRecoveryPreflightReasonCounts

        d = dict(src_dict)
        job_id = UUID(d.pop("job_id"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        state = check_event_recovery_preflight_state(d.pop("state"))

        active = d.pop("active")

        pending_count = d.pop("pending_count")

        eligible_count = d.pop("eligible_count")

        waiting_count = d.pop("waiting_count")

        likely_skipped_count = d.pop("likely_skipped_count")

        unknown_count = d.pop("unknown_count")

        reason_counts = EventRecoveryPreflightReasonCounts.from_dict(d.pop("reason_counts"))

        capacity_scopes = EventRecoveryPreflightCapacityScopes.from_dict(d.pop("capacity_scopes"))

        rate_per_second = d.pop("rate_per_second")

        remaining_lifetime_seconds = d.pop("remaining_lifetime_seconds")

        minimum_drain_seconds = d.pop("minimum_drain_seconds")

        earliest_drain_at = datetime.datetime.fromisoformat(d.pop("earliest_drain_at"))

        fits_before_expiry = d.pop("fits_before_expiry")

        assumes_immediate_resume = d.pop("assumes_immediate_resume")

        sample = []
        _sample = d.pop("sample")
        for sample_item_data in _sample:
            sample_item = EventRecoveryPreflightItem.from_dict(sample_item_data)

            sample.append(sample_item)

        receipt_retention_warning_count = d.pop("receipt_retention_warning_count", UNSET)

        receipt_retention_held_count = d.pop("receipt_retention_held_count", UNSET)

        _earliest_unheld_retain_until = d.pop("earliest_unheld_retain_until", UNSET)
        earliest_unheld_retain_until: datetime.datetime | Unset
        if isinstance(_earliest_unheld_retain_until, Unset):
            earliest_unheld_retain_until = UNSET
        else:
            earliest_unheld_retain_until = datetime.datetime.fromisoformat(_earliest_unheld_retain_until)

        minimum_drain_crosses_receipt_retention = d.pop("minimum_drain_crosses_receipt_retention", UNSET)

        event_recovery_preflight = cls(
            job_id=job_id,
            observed_at=observed_at,
            state=state,
            active=active,
            pending_count=pending_count,
            eligible_count=eligible_count,
            waiting_count=waiting_count,
            likely_skipped_count=likely_skipped_count,
            unknown_count=unknown_count,
            reason_counts=reason_counts,
            capacity_scopes=capacity_scopes,
            rate_per_second=rate_per_second,
            remaining_lifetime_seconds=remaining_lifetime_seconds,
            minimum_drain_seconds=minimum_drain_seconds,
            earliest_drain_at=earliest_drain_at,
            fits_before_expiry=fits_before_expiry,
            assumes_immediate_resume=assumes_immediate_resume,
            sample=sample,
            receipt_retention_warning_count=receipt_retention_warning_count,
            receipt_retention_held_count=receipt_retention_held_count,
            earliest_unheld_retain_until=earliest_unheld_retain_until,
            minimum_drain_crosses_receipt_retention=minimum_drain_crosses_receipt_retention,
        )

        return event_recovery_preflight
