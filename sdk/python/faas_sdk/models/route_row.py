from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RouteRow")


@_attrs_define
class RouteRow:
    """Per-route detail row (ADR-093). The `route` field is the
    bounded label: `"GET /users/4f8a"` for an admitted route, or
    `"__route_other__"` for the overflow bucket. Latency fields
    are milliseconds over the full request duration (status-
    agnostic — failures included). Same zero-on-degraded
    contract as `AppMetricsResponse`.

    """

    route: str
    """Bounded route label (method + raw path, or `__route_other__`)."""
    count: int
    """Number of requests with this route in the window."""
    p50_ms: float
    """p50 of `gateway_request_duration_seconds_bucket{app, route, class}` over the window, in ms."""
    p95_ms: float
    """p95 over all classes in the window, in ms."""
    p99_ms: float
    """p99 over all classes in the window, in ms."""
    error_pct: float
    """Share of 5xx requests with this route in the window. Client-caused 4xx responses do not consume availability
    budget."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        route = self.route

        count = self.count

        p50_ms = self.p50_ms

        p95_ms = self.p95_ms

        p99_ms = self.p99_ms

        error_pct = self.error_pct

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "route": route,
                "count": count,
                "p50_ms": p50_ms,
                "p95_ms": p95_ms,
                "p99_ms": p99_ms,
                "error_pct": error_pct,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        route = d.pop("route")

        count = d.pop("count")

        p50_ms = d.pop("p50_ms")

        p95_ms = d.pop("p95_ms")

        p99_ms = d.pop("p99_ms")

        error_pct = d.pop("error_pct")

        route_row = cls(
            route=route,
            count=count,
            p50_ms=p50_ms,
            p95_ms=p95_ms,
            p99_ms=p99_ms,
            error_pct=error_pct,
        )

        route_row.additional_properties = d
        return route_row

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
