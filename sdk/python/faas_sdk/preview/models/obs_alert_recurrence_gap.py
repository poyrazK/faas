from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ObsAlertRecurrenceGap")


@_attrs_define
class ObsAlertRecurrenceGap:
    """Adjacent-repeat partition; valid samples measure confirmed resolution observation to next activation."""

    samples: int
    unconfirmed_recovery: int
    resolution_overlap: int
    invalid_clock: int
    min_seconds: int | None
    p50_seconds: int | None
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        samples = self.samples

        unconfirmed_recovery = self.unconfirmed_recovery

        resolution_overlap = self.resolution_overlap

        invalid_clock = self.invalid_clock

        min_seconds: int | None
        min_seconds = self.min_seconds

        p50_seconds: int | None
        p50_seconds = self.p50_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "samples": samples,
                "unconfirmed_recovery": unconfirmed_recovery,
                "resolution_overlap": resolution_overlap,
                "invalid_clock": invalid_clock,
                "min_seconds": min_seconds,
                "p50_seconds": p50_seconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        samples = d.pop("samples")

        unconfirmed_recovery = d.pop("unconfirmed_recovery")

        resolution_overlap = d.pop("resolution_overlap")

        invalid_clock = d.pop("invalid_clock")

        def _parse_min_seconds(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        min_seconds = _parse_min_seconds(d.pop("min_seconds"))

        def _parse_p50_seconds(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        p50_seconds = _parse_p50_seconds(d.pop("p50_seconds"))

        obs_alert_recurrence_gap = cls(
            samples=samples,
            unconfirmed_recovery=unconfirmed_recovery,
            resolution_overlap=resolution_overlap,
            invalid_clock=invalid_clock,
            min_seconds=min_seconds,
            p50_seconds=p50_seconds,
        )

        obs_alert_recurrence_gap.additional_properties = d
        return obs_alert_recurrence_gap

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
