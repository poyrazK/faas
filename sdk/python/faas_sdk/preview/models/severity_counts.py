from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="SeverityCounts")


@_attrs_define
class SeverityCounts:
    """Per-bucket count of CVEs in Grype's closed vocabulary
    (CRITICAL|HIGH|MEDIUM|LOW|UNKNOWN). Negligible collapses into LOW
    (matches the existing pkg/imaged.grype.go::normalizeGrypeSeverity
    convention). All fields present without omitempty so the JSON shape
    is uniform — the dashboard reads counts without nil checks.

    """

    critical: int
    high: int
    medium: int
    low: int
    unknown: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        critical = self.critical

        high = self.high

        medium = self.medium

        low = self.low

        unknown = self.unknown

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "critical": critical,
                "high": high,
                "medium": medium,
                "low": low,
                "unknown": unknown,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        critical = d.pop("critical")

        high = d.pop("high")

        medium = d.pop("medium")

        low = d.pop("low")

        unknown = d.pop("unknown")

        severity_counts = cls(
            critical=critical,
            high=high,
            medium=medium,
            low=low,
            unknown=unknown,
        )

        severity_counts.additional_properties = d
        return severity_counts

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
