from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ObsRuntimeDelayedTaskDispatchCounts")


@_attrs_define
class ObsRuntimeDelayedTaskDispatchCounts:
    """Full-set signal coverage before preview clipping, with four counters per scheduler target. Increased, unchanged,
    baseline, missing, stale, conflicting and unavailable partition inspected signals. Observed counts usable current
    points, including unknown comparisons. These are signal classifications, not delta magnitudes or unique tasks.

    """

    expected: int
    observed: int
    increased: int
    unchanged: int
    baseline: int
    missing: int
    stale: int
    conflicting: int
    unavailable: int
    unexpected: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        expected = self.expected

        observed = self.observed

        increased = self.increased

        unchanged = self.unchanged

        baseline = self.baseline

        missing = self.missing

        stale = self.stale

        conflicting = self.conflicting

        unavailable = self.unavailable

        unexpected = self.unexpected

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "expected": expected,
                "observed": observed,
                "increased": increased,
                "unchanged": unchanged,
                "baseline": baseline,
                "missing": missing,
                "stale": stale,
                "conflicting": conflicting,
                "unavailable": unavailable,
                "unexpected": unexpected,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected = d.pop("expected")

        observed = d.pop("observed")

        increased = d.pop("increased")

        unchanged = d.pop("unchanged")

        baseline = d.pop("baseline")

        missing = d.pop("missing")

        stale = d.pop("stale")

        conflicting = d.pop("conflicting")

        unavailable = d.pop("unavailable")

        unexpected = d.pop("unexpected")

        obs_runtime_delayed_task_dispatch_counts = cls(
            expected=expected,
            observed=observed,
            increased=increased,
            unchanged=unchanged,
            baseline=baseline,
            missing=missing,
            stale=stale,
            conflicting=conflicting,
            unavailable=unavailable,
            unexpected=unexpected,
        )

        obs_runtime_delayed_task_dispatch_counts.additional_properties = d
        return obs_runtime_delayed_task_dispatch_counts

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
