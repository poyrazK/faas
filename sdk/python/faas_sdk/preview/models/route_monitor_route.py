from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.route_monitor_route_method import RouteMonitorRouteMethod, check_route_monitor_route_method
from ..types import UNSET, Unset

T = TypeVar("T", bound="RouteMonitorRoute")


@_attrs_define
class RouteMonitorRoute:
    """Exact normalized production telemetry label with absolute budgets. Omitted max_5xx_rate_bps disables errors; zero is
    a selected zero-error budget. Zero or omitted max_p95_ms disables latency. At least one budget is required.

    """

    method: RouteMonitorRouteMethod
    path: str
    max_5xx_rate_bps: int | Unset = UNSET
    max_p95_ms: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        method: str = self.method

        path = self.path

        max_5xx_rate_bps = self.max_5xx_rate_bps

        max_p95_ms = self.max_p95_ms

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "method": method,
                "path": path,
            }
        )
        if max_5xx_rate_bps is not UNSET:
            field_dict["max_5xx_rate_bps"] = max_5xx_rate_bps
        if max_p95_ms is not UNSET:
            field_dict["max_p95_ms"] = max_p95_ms

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        method = check_route_monitor_route_method(d.pop("method"))

        path = d.pop("path")

        max_5xx_rate_bps = d.pop("max_5xx_rate_bps", UNSET)

        max_p95_ms = d.pop("max_p95_ms", UNSET)

        route_monitor_route = cls(
            method=method,
            path=path,
            max_5xx_rate_bps=max_5xx_rate_bps,
            max_p95_ms=max_p95_ms,
        )

        return route_monitor_route
