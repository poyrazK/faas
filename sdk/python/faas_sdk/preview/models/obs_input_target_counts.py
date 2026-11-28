from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ObsInputTargetCounts")


@_attrs_define
class ObsInputTargetCounts:
    """Original scrape freshness partition; missing minimum is separate from observed target failures."""

    observed: int
    fresh_up: int
    down: int
    stale: int
    missing_minimum: int | None
    """Expected minimum less observed count, clamped at zero; null if the expectation is unknown."""
    extra: int | None
    """Observed count above fresh discovery count; null for static minimums or unknown expectations."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        observed = self.observed

        fresh_up = self.fresh_up

        down = self.down

        stale = self.stale

        missing_minimum: int | None
        missing_minimum = self.missing_minimum

        extra: int | None
        extra = self.extra

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "observed": observed,
                "fresh_up": fresh_up,
                "down": down,
                "stale": stale,
                "missing_minimum": missing_minimum,
                "extra": extra,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        observed = d.pop("observed")

        fresh_up = d.pop("fresh_up")

        down = d.pop("down")

        stale = d.pop("stale")

        def _parse_missing_minimum(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        missing_minimum = _parse_missing_minimum(d.pop("missing_minimum"))

        def _parse_extra(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        extra = _parse_extra(d.pop("extra"))

        obs_input_target_counts = cls(
            observed=observed,
            fresh_up=fresh_up,
            down=down,
            stale=stale,
            missing_minimum=missing_minimum,
            extra=extra,
        )

        obs_input_target_counts.additional_properties = d
        return obs_input_target_counts

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
