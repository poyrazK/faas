from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_command_cron_timing_name import (
    ObsRuntimeCommandCronTimingName,
    check_obs_runtime_command_cron_timing_name,
)

T = TypeVar("T", bound="ObsRuntimeCommandCronTimingSignalSummary")


@_attrs_define
class ObsRuntimeCommandCronTimingSignalSummary:
    """Full-set target classifications for one closed timing signal; counts are independent of preview clipping."""

    signal: ObsRuntimeCommandCronTimingName
    """Fixed cumulative upper-bound buckets in seconds, followed by the histogram count (timed) and an independent
    unknown-timestamp counter. le_inf equals timed. Buckets overlap and must not be summed."""
    increased: int
    unchanged: int
    baseline: int
    unknown: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        signal: str = self.signal

        increased = self.increased

        unchanged = self.unchanged

        baseline = self.baseline

        unknown = self.unknown

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "signal": signal,
                "increased": increased,
                "unchanged": unchanged,
                "baseline": baseline,
                "unknown": unknown,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        signal = check_obs_runtime_command_cron_timing_name(d.pop("signal"))

        increased = d.pop("increased")

        unchanged = d.pop("unchanged")

        baseline = d.pop("baseline")

        unknown = d.pop("unknown")

        obs_runtime_command_cron_timing_signal_summary = cls(
            signal=signal,
            increased=increased,
            unchanged=unchanged,
            baseline=baseline,
            unknown=unknown,
        )

        obs_runtime_command_cron_timing_signal_summary.additional_properties = d
        return obs_runtime_command_cron_timing_signal_summary

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
