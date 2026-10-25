from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.data_repair_phase_health_phase import DataRepairPhaseHealthPhase, check_data_repair_phase_health_phase
from ..types import UNSET, Unset

T = TypeVar("T", bound="DataRepairPhaseHealth")


@_attrs_define
class DataRepairPhaseHealth:
    """Global count and oldest durable phase clock, independent of detail pagination."""

    phase: DataRepairPhaseHealthPhase
    count: int
    oldest_age_seconds: int
    warn_after_seconds: int
    """Unknown apply outcomes require attention immediately."""
    oldest_phase_started_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        phase: str = self.phase

        count = self.count

        oldest_age_seconds = self.oldest_age_seconds

        warn_after_seconds = self.warn_after_seconds

        oldest_phase_started_at: str | Unset = UNSET
        if not isinstance(self.oldest_phase_started_at, Unset):
            oldest_phase_started_at = self.oldest_phase_started_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "phase": phase,
                "count": count,
                "oldest_age_seconds": oldest_age_seconds,
                "warn_after_seconds": warn_after_seconds,
            }
        )
        if oldest_phase_started_at is not UNSET:
            field_dict["oldest_phase_started_at"] = oldest_phase_started_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        phase = check_data_repair_phase_health_phase(d.pop("phase"))

        count = d.pop("count")

        oldest_age_seconds = d.pop("oldest_age_seconds")

        warn_after_seconds = d.pop("warn_after_seconds")

        _oldest_phase_started_at = d.pop("oldest_phase_started_at", UNSET)
        oldest_phase_started_at: datetime.datetime | Unset
        if isinstance(_oldest_phase_started_at, Unset):
            oldest_phase_started_at = UNSET
        else:
            oldest_phase_started_at = datetime.datetime.fromisoformat(_oldest_phase_started_at)

        data_repair_phase_health = cls(
            phase=phase,
            count=count,
            oldest_age_seconds=oldest_age_seconds,
            warn_after_seconds=warn_after_seconds,
            oldest_phase_started_at=oldest_phase_started_at,
        )

        data_repair_phase_health.additional_properties = d
        return data_repair_phase_health

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
