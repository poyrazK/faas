from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RequestAnalyticsTimeseriesPoint")


@_attrs_define
class RequestAnalyticsTimeseriesPoint:
    """One UTC-aligned hourly request analytics bucket."""

    start: datetime.datetime
    """Inclusive UTC start of the one-hour bucket."""
    requests: int
    error_requests: int
    error_rate_pct: float
    cold_boots: int
    p50_ms: int
    p95_ms: int
    p99_ms: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        start = self.start.isoformat()

        requests = self.requests

        error_requests = self.error_requests

        error_rate_pct = self.error_rate_pct

        cold_boots = self.cold_boots

        p50_ms = self.p50_ms

        p95_ms = self.p95_ms

        p99_ms = self.p99_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "start": start,
                "requests": requests,
                "error_requests": error_requests,
                "error_rate_pct": error_rate_pct,
                "cold_boots": cold_boots,
                "p50_ms": p50_ms,
                "p95_ms": p95_ms,
                "p99_ms": p99_ms,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        start = datetime.datetime.fromisoformat(d.pop("start"))

        requests = d.pop("requests")

        error_requests = d.pop("error_requests")

        error_rate_pct = d.pop("error_rate_pct")

        cold_boots = d.pop("cold_boots")

        p50_ms = d.pop("p50_ms")

        p95_ms = d.pop("p95_ms")

        p99_ms = d.pop("p99_ms")

        request_analytics_timeseries_point = cls(
            start=start,
            requests=requests,
            error_requests=error_requests,
            error_rate_pct=error_rate_pct,
            cold_boots=cold_boots,
            p50_ms=p50_ms,
            p95_ms=p95_ms,
            p99_ms=p99_ms,
        )

        request_analytics_timeseries_point.additional_properties = d
        return request_analytics_timeseries_point

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
