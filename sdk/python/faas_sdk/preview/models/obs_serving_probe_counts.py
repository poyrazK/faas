from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ObsServingProbeCounts")


@_attrs_define
class ObsServingProbeCounts:
    """Counts cover every expected pair. Reporting is known true only for evaluated pairs with a result; unknown
    evaluations never count as healthy or reporting.

    """

    expected: int
    reporting: int
    healthy: int
    missing: int
    stale: int
    invalid: int
    content_disabled: int
    budget_mismatch: int
    failing: int
    unknown: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        expected = self.expected

        reporting = self.reporting

        healthy = self.healthy

        missing = self.missing

        stale = self.stale

        invalid = self.invalid

        content_disabled = self.content_disabled

        budget_mismatch = self.budget_mismatch

        failing = self.failing

        unknown = self.unknown

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "expected": expected,
                "reporting": reporting,
                "healthy": healthy,
                "missing": missing,
                "stale": stale,
                "invalid": invalid,
                "content_disabled": content_disabled,
                "budget_mismatch": budget_mismatch,
                "failing": failing,
                "unknown": unknown,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected = d.pop("expected")

        reporting = d.pop("reporting")

        healthy = d.pop("healthy")

        missing = d.pop("missing")

        stale = d.pop("stale")

        invalid = d.pop("invalid")

        content_disabled = d.pop("content_disabled")

        budget_mismatch = d.pop("budget_mismatch")

        failing = d.pop("failing")

        unknown = d.pop("unknown")

        obs_serving_probe_counts = cls(
            expected=expected,
            reporting=reporting,
            healthy=healthy,
            missing=missing,
            stale=stale,
            invalid=invalid,
            content_disabled=content_disabled,
            budget_mismatch=budget_mismatch,
            failing=failing,
            unknown=unknown,
        )

        obs_serving_probe_counts.additional_properties = d
        return obs_serving_probe_counts

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
