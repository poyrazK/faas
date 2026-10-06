from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_health_route_method import RouteHealthRouteMethod, check_route_health_route_method
from ..models.route_health_route_watch_statuses_item import (
    RouteHealthRouteWatchStatusesItem,
    check_route_health_route_watch_statuses_item,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="RouteHealthRoute")


@_attrs_define
class RouteHealthRoute:
    """Exact normalized telemetry operation selected for canary error checks and optional latency checks."""

    method: RouteHealthRouteMethod
    path: str
    """Exact gateway-normalized telemetry path without method prefix, query, fragment or wildcard. For example
    /profiles/{id}."""
    watch_statuses: list[RouteHealthRouteWatchStatusesItem] | Unset = UNSET
    """Opt-in live advisory comparisons, evaluated independently per code. Omission or an empty array disables
    them. Findings do not alter 5xx/latency gates or automatic recovery. Replacement edits increment revision and
    require fresh windows."""
    check_latency: bool | Unset = UNSET
    """Opt in to the relative p95 slowdown check (at least 1.5 times stable and 100 ms slower). Omitted or false
    disables this check independently of max_p95_ms."""
    max_p95_ms: int | Unset = UNSET
    """Absolute candidate p95 latency budget in milliseconds. A positive value enables this check; omitted or zero
    disables it. Does not enable the relative slowdown check."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        method: str = self.method

        path = self.path

        watch_statuses: list[int] | Unset = UNSET
        if not isinstance(self.watch_statuses, Unset):
            watch_statuses = []
            for watch_statuses_item_data in self.watch_statuses:
                watch_statuses_item: int = watch_statuses_item_data
                watch_statuses.append(watch_statuses_item)

        check_latency = self.check_latency

        max_p95_ms = self.max_p95_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "method": method,
                "path": path,
            }
        )
        if watch_statuses is not UNSET:
            field_dict["watch_statuses"] = watch_statuses
        if check_latency is not UNSET:
            field_dict["check_latency"] = check_latency
        if max_p95_ms is not UNSET:
            field_dict["max_p95_ms"] = max_p95_ms

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        method = check_route_health_route_method(d.pop("method"))

        path = d.pop("path")

        _watch_statuses = d.pop("watch_statuses", UNSET)
        watch_statuses: list[RouteHealthRouteWatchStatusesItem] | Unset = UNSET
        if _watch_statuses is not UNSET:
            watch_statuses = []
            for watch_statuses_item_data in _watch_statuses:
                watch_statuses_item = check_route_health_route_watch_statuses_item(watch_statuses_item_data)

                watch_statuses.append(watch_statuses_item)

        check_latency = d.pop("check_latency", UNSET)

        max_p95_ms = d.pop("max_p95_ms", UNSET)

        route_health_route = cls(
            method=method,
            path=path,
            watch_statuses=watch_statuses,
            check_latency=check_latency,
            max_p95_ms=max_p95_ms,
        )

        route_health_route.additional_properties = d
        return route_health_route

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
