from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ObsAlertRecurrenceDuration")


@_attrs_define
class ObsAlertRecurrenceDuration:
    """Episode partition and valid activation-to-resolution observation statistics."""

    samples: int
    not_observed: int
    invalid_clock: int
    min_seconds: int | None
    p50_seconds: int | None
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        samples = self.samples

        not_observed = self.not_observed

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
                "not_observed": not_observed,
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

        not_observed = d.pop("not_observed")

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

        obs_alert_recurrence_duration = cls(
            samples=samples,
            not_observed=not_observed,
            invalid_clock=invalid_clock,
            min_seconds=min_seconds,
            p50_seconds=p50_seconds,
        )

        obs_alert_recurrence_duration.additional_properties = d
        return obs_alert_recurrence_duration

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
