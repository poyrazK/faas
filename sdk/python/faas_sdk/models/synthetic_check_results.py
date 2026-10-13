from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.synthetic_check_run import SyntheticCheckRun


T = TypeVar("T", bound="SyntheticCheckResults")


@_attrs_define
class SyntheticCheckResults:
    """Recent outcomes of one synthetic check, returned by GET /v1/apps/{slug}/synthetics/{id} (ADR-748)."""

    uptime_24h_pct: float | None
    """Share of runs in the last 24 hours that succeeded; null when none ran."""
    uptime_7d_pct: float | None
    """Share of runs in the last 7 days that succeeded; null when none ran."""
    runs_24h: int
    """Runs in the last 24 hours."""
    p95_latency_ms_24h: float
    """95th-percentile latency of successful runs in the last 24 hours, including any wake; 0 when none succeeded."""
    recent: list[SyntheticCheckRun]
    """The most recent runs, newest first (at most 20)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        uptime_24h_pct: float | None
        uptime_24h_pct = self.uptime_24h_pct

        uptime_7d_pct: float | None
        uptime_7d_pct = self.uptime_7d_pct

        runs_24h = self.runs_24h

        p95_latency_ms_24h = self.p95_latency_ms_24h

        recent = []
        for recent_item_data in self.recent:
            recent_item = recent_item_data.to_dict()
            recent.append(recent_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "uptime_24h_pct": uptime_24h_pct,
                "uptime_7d_pct": uptime_7d_pct,
                "runs_24h": runs_24h,
                "p95_latency_ms_24h": p95_latency_ms_24h,
                "recent": recent,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.synthetic_check_run import SyntheticCheckRun

        d = dict(src_dict)

        def _parse_uptime_24h_pct(data: object) -> float | None:
            if data is None:
                return data
            return cast(float | None, data)

        uptime_24h_pct = _parse_uptime_24h_pct(d.pop("uptime_24h_pct"))

        def _parse_uptime_7d_pct(data: object) -> float | None:
            if data is None:
                return data
            return cast(float | None, data)

        uptime_7d_pct = _parse_uptime_7d_pct(d.pop("uptime_7d_pct"))

        runs_24h = d.pop("runs_24h")

        p95_latency_ms_24h = d.pop("p95_latency_ms_24h")

        recent = []
        _recent = d.pop("recent")
        for recent_item_data in _recent:
            recent_item = SyntheticCheckRun.from_dict(recent_item_data)

            recent.append(recent_item)

        synthetic_check_results = cls(
            uptime_24h_pct=uptime_24h_pct,
            uptime_7d_pct=uptime_7d_pct,
            runs_24h=runs_24h,
            p95_latency_ms_24h=p95_latency_ms_24h,
            recent=recent,
        )

        synthetic_check_results.additional_properties = d
        return synthetic_check_results

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
