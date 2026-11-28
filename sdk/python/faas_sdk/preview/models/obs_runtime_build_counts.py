from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ObsRuntimeBuildCounts")


@_attrs_define
class ObsRuntimeBuildCounts:
    """State counts partition all inspected items; expected, observed, unexpected and distinct_revisions overlap these
    counts. Unavailable jobs can have unknown expected counts; inspect jobs.

    """

    expected: int
    observed: int
    matching: int
    mismatched: int
    missing: int
    stale: int
    unavailable: int
    unexpected: int
    distinct_revisions: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        expected = self.expected

        observed = self.observed

        matching = self.matching

        mismatched = self.mismatched

        missing = self.missing

        stale = self.stale

        unavailable = self.unavailable

        unexpected = self.unexpected

        distinct_revisions = self.distinct_revisions

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "expected": expected,
                "observed": observed,
                "matching": matching,
                "mismatched": mismatched,
                "missing": missing,
                "stale": stale,
                "unavailable": unavailable,
                "unexpected": unexpected,
                "distinct_revisions": distinct_revisions,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected = d.pop("expected")

        observed = d.pop("observed")

        matching = d.pop("matching")

        mismatched = d.pop("mismatched")

        missing = d.pop("missing")

        stale = d.pop("stale")

        unavailable = d.pop("unavailable")

        unexpected = d.pop("unexpected")

        distinct_revisions = d.pop("distinct_revisions")

        obs_runtime_build_counts = cls(
            expected=expected,
            observed=observed,
            matching=matching,
            mismatched=mismatched,
            missing=missing,
            stale=stale,
            unavailable=unavailable,
            unexpected=unexpected,
            distinct_revisions=distinct_revisions,
        )

        obs_runtime_build_counts.additional_properties = d
        return obs_runtime_build_counts

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
