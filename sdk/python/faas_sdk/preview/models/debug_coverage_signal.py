from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="DebugCoverageSignal")


@_attrs_define
class DebugCoverageSignal:
    """Observed coverage for one debugger signal. Requests are weighted by collapsed-row count."""

    rows: int
    requests: int
    rate_pct: float
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        rows = self.rows

        requests = self.requests

        rate_pct = self.rate_pct

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "rows": rows,
                "requests": requests,
                "rate_pct": rate_pct,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        rows = d.pop("rows")

        requests = d.pop("requests")

        rate_pct = d.pop("rate_pct")

        debug_coverage_signal = cls(
            rows=rows,
            requests=requests,
            rate_pct=rate_pct,
        )

        debug_coverage_signal.additional_properties = d
        return debug_coverage_signal

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
