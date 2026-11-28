from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ObsRuntimeLoopCounts")


@_attrs_define
class ObsRuntimeLoopCounts:
    """State counts partition inspected target-loop pairs. Expected and observed counts overlap the partition;
    reported_stalls includes fresh stall reports with incomplete age evidence.

    """

    expected: int
    observed: int
    no_reported_stall: int
    stalled: int
    missing: int
    stale: int
    conflicting: int
    unavailable: int
    reported_stalls: int
    unexpected: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        expected = self.expected

        observed = self.observed

        no_reported_stall = self.no_reported_stall

        stalled = self.stalled

        missing = self.missing

        stale = self.stale

        conflicting = self.conflicting

        unavailable = self.unavailable

        reported_stalls = self.reported_stalls

        unexpected = self.unexpected

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "expected": expected,
                "observed": observed,
                "no_reported_stall": no_reported_stall,
                "stalled": stalled,
                "missing": missing,
                "stale": stale,
                "conflicting": conflicting,
                "unavailable": unavailable,
                "reported_stalls": reported_stalls,
                "unexpected": unexpected,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected = d.pop("expected")

        observed = d.pop("observed")

        no_reported_stall = d.pop("no_reported_stall")

        stalled = d.pop("stalled")

        missing = d.pop("missing")

        stale = d.pop("stale")

        conflicting = d.pop("conflicting")

        unavailable = d.pop("unavailable")

        reported_stalls = d.pop("reported_stalls")

        unexpected = d.pop("unexpected")

        obs_runtime_loop_counts = cls(
            expected=expected,
            observed=observed,
            no_reported_stall=no_reported_stall,
            stalled=stalled,
            missing=missing,
            stale=stale,
            conflicting=conflicting,
            unavailable=unavailable,
            reported_stalls=reported_stalls,
            unexpected=unexpected,
        )

        obs_runtime_loop_counts.additional_properties = d
        return obs_runtime_loop_counts

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
