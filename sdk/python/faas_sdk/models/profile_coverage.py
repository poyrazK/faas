from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.profile_attribution_reason import ProfileAttributionReason


T = TypeVar("T", bound="ProfileCoverage")


@_attrs_define
class ProfileCoverage:
    """Recorded collection evidence. Overlapping intervals count once; gaps can include idle time or loss. Failure counts
    are a lower bound; losses before ingestion are unknown. Unavailable coverage must not be interpreted as zero
    collection.

    """

    available: bool
    received_profiles: int
    contributing_collectors: int
    window_seconds: float
    covered_seconds: float
    gap_seconds: float
    recorded_failed_uploads: int
    """Rate limited recorded failures; a lower bound on failed attempts rather than exact lost profiles."""
    failures_complete: bool
    """False because failures before ingestion and omitted failure records are unknown."""
    attribution_reasons: list[ProfileAttributionReason] | Unset = UNSET
    """Optional host-generated whole-capture CPU diagnostics. Legacy captures have no counters; public attribution
    quality reconciles counters against merged CPU before declaring completeness."""
    last_received_at: datetime.datetime | Unset = UNSET
    """Latest recorded receipt among profiles in the selected capture window."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        available = self.available

        received_profiles = self.received_profiles

        contributing_collectors = self.contributing_collectors

        window_seconds = self.window_seconds

        covered_seconds = self.covered_seconds

        gap_seconds = self.gap_seconds

        recorded_failed_uploads = self.recorded_failed_uploads

        failures_complete = self.failures_complete

        attribution_reasons: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.attribution_reasons, Unset):
            attribution_reasons = []
            for attribution_reasons_item_data in self.attribution_reasons:
                attribution_reasons_item = attribution_reasons_item_data.to_dict()
                attribution_reasons.append(attribution_reasons_item)

        last_received_at: str | Unset = UNSET
        if not isinstance(self.last_received_at, Unset):
            last_received_at = self.last_received_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "available": available,
                "received_profiles": received_profiles,
                "contributing_collectors": contributing_collectors,
                "window_seconds": window_seconds,
                "covered_seconds": covered_seconds,
                "gap_seconds": gap_seconds,
                "recorded_failed_uploads": recorded_failed_uploads,
                "failures_complete": failures_complete,
            }
        )
        if attribution_reasons is not UNSET:
            field_dict["attribution_reasons"] = attribution_reasons
        if last_received_at is not UNSET:
            field_dict["last_received_at"] = last_received_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_attribution_reason import ProfileAttributionReason

        d = dict(src_dict)
        available = d.pop("available")

        received_profiles = d.pop("received_profiles")

        contributing_collectors = d.pop("contributing_collectors")

        window_seconds = d.pop("window_seconds")

        covered_seconds = d.pop("covered_seconds")

        gap_seconds = d.pop("gap_seconds")

        recorded_failed_uploads = d.pop("recorded_failed_uploads")

        failures_complete = d.pop("failures_complete")

        _attribution_reasons = d.pop("attribution_reasons", UNSET)
        attribution_reasons: list[ProfileAttributionReason] | Unset = UNSET
        if _attribution_reasons is not UNSET:
            attribution_reasons = []
            for attribution_reasons_item_data in _attribution_reasons:
                attribution_reasons_item = ProfileAttributionReason.from_dict(attribution_reasons_item_data)

                attribution_reasons.append(attribution_reasons_item)

        _last_received_at = d.pop("last_received_at", UNSET)
        last_received_at: datetime.datetime | Unset
        if isinstance(_last_received_at, Unset):
            last_received_at = UNSET
        else:
            last_received_at = datetime.datetime.fromisoformat(_last_received_at)

        profile_coverage = cls(
            available=available,
            received_profiles=received_profiles,
            contributing_collectors=contributing_collectors,
            window_seconds=window_seconds,
            covered_seconds=covered_seconds,
            gap_seconds=gap_seconds,
            recorded_failed_uploads=recorded_failed_uploads,
            failures_complete=failures_complete,
            attribution_reasons=attribution_reasons,
            last_received_at=last_received_at,
        )

        profile_coverage.additional_properties = d
        return profile_coverage

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
