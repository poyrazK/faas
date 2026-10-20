from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_monitor_window_error_status import (
    RouteMonitorWindowErrorStatus,
    check_route_monitor_window_error_status,
)
from ..models.route_monitor_window_latency_status import (
    RouteMonitorWindowLatencyStatus,
    check_route_monitor_window_latency_status,
)

if TYPE_CHECKING:
    from ..models.route_health_counts import RouteHealthCounts


T = TypeVar("T", bound="RouteMonitorWindow")


@_attrs_define
class RouteMonitorWindow:
    """Observed weighted counts and independently evaluated absolute budgets in a closed window."""

    start: datetime.datetime
    end: datetime.datetime
    observed: RouteHealthCounts
    """Represented request and server-error counts, error rate, and optional weighted p95 for one deployment in one
    window."""
    error_status: RouteMonitorWindowErrorStatus
    error_reason: str
    latency_status: RouteMonitorWindowLatencyStatus
    latency_reason: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        start = self.start.isoformat()

        end = self.end.isoformat()

        observed = self.observed.to_dict()

        error_status: str = self.error_status

        error_reason = self.error_reason

        latency_status: str = self.latency_status

        latency_reason = self.latency_reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "start": start,
                "end": end,
                "observed": observed,
                "error_status": error_status,
                "error_reason": error_reason,
                "latency_status": latency_status,
                "latency_reason": latency_reason,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_health_counts import RouteHealthCounts

        d = dict(src_dict)
        start = datetime.datetime.fromisoformat(d.pop("start"))

        end = datetime.datetime.fromisoformat(d.pop("end"))

        observed = RouteHealthCounts.from_dict(d.pop("observed"))

        error_status = check_route_monitor_window_error_status(d.pop("error_status"))

        error_reason = d.pop("error_reason")

        latency_status = check_route_monitor_window_latency_status(d.pop("latency_status"))

        latency_reason = d.pop("latency_reason")

        route_monitor_window = cls(
            start=start,
            end=end,
            observed=observed,
            error_status=error_status,
            error_reason=error_reason,
            latency_status=latency_status,
            latency_reason=latency_reason,
        )

        route_monitor_window.additional_properties = d
        return route_monitor_window

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
