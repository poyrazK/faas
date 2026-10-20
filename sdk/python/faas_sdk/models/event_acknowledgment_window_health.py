from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, Literal, TypeVar, cast

from attrs import define as _attrs_define

from ..models.event_acknowledgment_window_health_coverage import (
    EventAcknowledgmentWindowHealthCoverage,
    check_event_acknowledgment_window_health_coverage,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_acknowledgment_recording_coverage import EventAcknowledgmentRecordingCoverage


T = TypeVar("T", bound="EventAcknowledgmentWindowHealth")


@_attrs_define
class EventAcknowledgmentWindowHealth:
    """Recorded renewal and expiry operations in full UTC minute buckets with optional verification of deployment-declared
    writer continuity.

    """

    window_start: datetime.datetime
    """Inclusive boundary of full minutes wholly inside the requested processing window."""
    window_end: datetime.datetime
    """Exclusive completed-minute boundary capped by the verified writer heartbeat watermark when available."""
    bucket_seconds: Literal[60]
    """Fixed width of each durable operation bucket."""
    retention_seconds: Literal[172800]
    """Metric retention measured from bucket start independently of invocation pruning."""
    coverage: EventAcknowledgmentWindowHealthCoverage
    """Only instrumented writers contribute; inspect recording_coverage for declared-inventory continuity and no
    pre-upgrade activity is reconstructed."""
    renewal_successes: int
    """Explicit successful renewals recorded in selected complete buckets."""
    renewal_rejections: int
    """Current-token renewal conflicts recorded after token and attempt verification."""
    renewal_attempts: int
    """Sum of recorded successful and rejected renewals used as the fraction denominator."""
    lease_expiries: int
    """Recorded deferred-awaiting scheduler expiry transitions in these minutes."""
    saturated: bool
    """At least one bucket overflowed its saturating counter; totals are lower bounds and rate fields are omitted."""
    recording_coverage: EventAcknowledgmentRecordingCoverage | Unset = UNSET
    """Capability and heartbeat verification against the exhaustive deployment-declared writer roster; not
    automatic discovery of unlisted processes or proof of metric data integrity."""
    renewal_rejection_fraction: float | Unset = UNSET
    """Recorded rejected renewals divided by recorded renewal attempts; absent without samples or complete minutes
    or after saturation."""
    lease_expiry_rate_per_second: float | Unset = UNSET
    """Recorded lease expiry transitions divided by the effective window duration; absent for an empty or saturated
    window."""

    def to_dict(self) -> dict[str, Any]:
        window_start = self.window_start.isoformat()

        window_end = self.window_end.isoformat()

        bucket_seconds = self.bucket_seconds

        retention_seconds = self.retention_seconds

        coverage: str = self.coverage

        renewal_successes = self.renewal_successes

        renewal_rejections = self.renewal_rejections

        renewal_attempts = self.renewal_attempts

        lease_expiries = self.lease_expiries

        saturated = self.saturated

        recording_coverage: dict[str, Any] | Unset = UNSET
        if not isinstance(self.recording_coverage, Unset):
            recording_coverage = self.recording_coverage.to_dict()

        renewal_rejection_fraction = self.renewal_rejection_fraction

        lease_expiry_rate_per_second = self.lease_expiry_rate_per_second

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "window_start": window_start,
                "window_end": window_end,
                "bucket_seconds": bucket_seconds,
                "retention_seconds": retention_seconds,
                "coverage": coverage,
                "renewal_successes": renewal_successes,
                "renewal_rejections": renewal_rejections,
                "renewal_attempts": renewal_attempts,
                "lease_expiries": lease_expiries,
                "saturated": saturated,
            }
        )
        if recording_coverage is not UNSET:
            field_dict["recording_coverage"] = recording_coverage
        if renewal_rejection_fraction is not UNSET:
            field_dict["renewal_rejection_fraction"] = renewal_rejection_fraction
        if lease_expiry_rate_per_second is not UNSET:
            field_dict["lease_expiry_rate_per_second"] = lease_expiry_rate_per_second

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_acknowledgment_recording_coverage import EventAcknowledgmentRecordingCoverage

        d = dict(src_dict)
        window_start = datetime.datetime.fromisoformat(d.pop("window_start"))

        window_end = datetime.datetime.fromisoformat(d.pop("window_end"))

        bucket_seconds = cast(Literal[60], d.pop("bucket_seconds"))
        if bucket_seconds != 60:
            raise ValueError(f"bucket_seconds must match const 60, got '{bucket_seconds}'")

        retention_seconds = cast(Literal[172800], d.pop("retention_seconds"))
        if retention_seconds != 172800:
            raise ValueError(f"retention_seconds must match const 172800, got '{retention_seconds}'")

        coverage = check_event_acknowledgment_window_health_coverage(d.pop("coverage"))

        renewal_successes = d.pop("renewal_successes")

        renewal_rejections = d.pop("renewal_rejections")

        renewal_attempts = d.pop("renewal_attempts")

        lease_expiries = d.pop("lease_expiries")

        saturated = d.pop("saturated")

        _recording_coverage = d.pop("recording_coverage", UNSET)
        recording_coverage: EventAcknowledgmentRecordingCoverage | Unset
        if isinstance(_recording_coverage, Unset):
            recording_coverage = UNSET
        else:
            recording_coverage = EventAcknowledgmentRecordingCoverage.from_dict(_recording_coverage)

        renewal_rejection_fraction = d.pop("renewal_rejection_fraction", UNSET)

        lease_expiry_rate_per_second = d.pop("lease_expiry_rate_per_second", UNSET)

        event_acknowledgment_window_health = cls(
            window_start=window_start,
            window_end=window_end,
            bucket_seconds=bucket_seconds,
            retention_seconds=retention_seconds,
            coverage=coverage,
            renewal_successes=renewal_successes,
            renewal_rejections=renewal_rejections,
            renewal_attempts=renewal_attempts,
            lease_expiries=lease_expiries,
            saturated=saturated,
            recording_coverage=recording_coverage,
            renewal_rejection_fraction=renewal_rejection_fraction,
            lease_expiry_rate_per_second=lease_expiry_rate_per_second,
        )

        return event_acknowledgment_window_health
