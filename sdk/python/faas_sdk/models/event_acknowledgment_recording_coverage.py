from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, Literal, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="EventAcknowledgmentRecordingCoverage")


@_attrs_define
class EventAcknowledgmentRecordingCoverage:
    """Capability and heartbeat verification against the exhaustive deployment-declared writer roster; not automatic
    discovery of unlisted processes or proof of metric data integrity.

    """

    verified: bool
    """All declared slots have one live compatible session and a continuous heartbeat watermark."""
    reason: str
    """Bounded coverage diagnostic such as verified or missing_writer or inventory_unconfigured or warming_up."""
    observed_at: datetime.datetime
    """Database clock used to assess writer leases in the metric read snapshot."""
    expected_writers: int
    """Declared members when a usable inventory is available."""
    active_writers: int
    """Bounded live session count; 257 denotes evidence exceeding the scan limit."""
    capability_version: Literal[1]
    """Required deferred metric recording capability protocol."""
    heartbeat_lease_seconds: Literal[30]
    """Writer heartbeat lease horizon assessed using database time."""
    continuous_since: datetime.datetime | Unset = UNSET
    """Conservative continuity boundary reset by observed membership changes or heartbeat gaps."""
    verified_through: datetime.datetime | Unset = UNSET
    """Least recent live writer heartbeat; no later minute is considered verified."""
    inventory_revision: str | Unset = UNSET
    """Opaque hash of the common canonical declared inventory without exposing slots or sessions."""

    def to_dict(self) -> dict[str, Any]:
        verified = self.verified

        reason = self.reason

        observed_at = self.observed_at.isoformat()

        expected_writers = self.expected_writers

        active_writers = self.active_writers

        capability_version = self.capability_version

        heartbeat_lease_seconds = self.heartbeat_lease_seconds

        continuous_since: str | Unset = UNSET
        if not isinstance(self.continuous_since, Unset):
            continuous_since = self.continuous_since.isoformat()

        verified_through: str | Unset = UNSET
        if not isinstance(self.verified_through, Unset):
            verified_through = self.verified_through.isoformat()

        inventory_revision = self.inventory_revision

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "verified": verified,
                "reason": reason,
                "observed_at": observed_at,
                "expected_writers": expected_writers,
                "active_writers": active_writers,
                "capability_version": capability_version,
                "heartbeat_lease_seconds": heartbeat_lease_seconds,
            }
        )
        if continuous_since is not UNSET:
            field_dict["continuous_since"] = continuous_since
        if verified_through is not UNSET:
            field_dict["verified_through"] = verified_through
        if inventory_revision is not UNSET:
            field_dict["inventory_revision"] = inventory_revision

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        verified = d.pop("verified")

        reason = d.pop("reason")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        expected_writers = d.pop("expected_writers")

        active_writers = d.pop("active_writers")

        capability_version = cast(Literal[1], d.pop("capability_version"))
        if capability_version != 1:
            raise ValueError(f"capability_version must match const 1, got '{capability_version}'")

        heartbeat_lease_seconds = cast(Literal[30], d.pop("heartbeat_lease_seconds"))
        if heartbeat_lease_seconds != 30:
            raise ValueError(f"heartbeat_lease_seconds must match const 30, got '{heartbeat_lease_seconds}'")

        _continuous_since = d.pop("continuous_since", UNSET)
        continuous_since: datetime.datetime | Unset
        if isinstance(_continuous_since, Unset):
            continuous_since = UNSET
        else:
            continuous_since = datetime.datetime.fromisoformat(_continuous_since)

        _verified_through = d.pop("verified_through", UNSET)
        verified_through: datetime.datetime | Unset
        if isinstance(_verified_through, Unset):
            verified_through = UNSET
        else:
            verified_through = datetime.datetime.fromisoformat(_verified_through)

        inventory_revision = d.pop("inventory_revision", UNSET)

        event_acknowledgment_recording_coverage = cls(
            verified=verified,
            reason=reason,
            observed_at=observed_at,
            expected_writers=expected_writers,
            active_writers=active_writers,
            capability_version=capability_version,
            heartbeat_lease_seconds=heartbeat_lease_seconds,
            continuous_since=continuous_since,
            verified_through=verified_through,
            inventory_revision=inventory_revision,
        )

        return event_acknowledgment_recording_coverage
