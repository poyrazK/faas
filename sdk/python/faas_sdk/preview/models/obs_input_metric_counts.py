from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ObsInputMetricCounts")


@_attrs_define
class ObsInputMetricCounts:
    """Per-target sample coverage before aggregation; freshness uses original sample times in bounded history."""

    expected: int
    fresh: int
    missing: int
    stale: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        expected = self.expected

        fresh = self.fresh

        missing = self.missing

        stale = self.stale

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "expected": expected,
                "fresh": fresh,
                "missing": missing,
                "stale": stale,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected = d.pop("expected")

        fresh = d.pop("fresh")

        missing = d.pop("missing")

        stale = d.pop("stale")

        obs_input_metric_counts = cls(
            expected=expected,
            fresh=fresh,
            missing=missing,
            stale=stale,
        )

        obs_input_metric_counts.additional_properties = d
        return obs_input_metric_counts

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
