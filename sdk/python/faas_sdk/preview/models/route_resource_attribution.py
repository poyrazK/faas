from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RouteResourceAttribution")


@_attrs_define
class RouteResourceAttribution:
    """One route's share of the window's memory and CPU peaks."""

    route: str
    method: str
    requests: int
    busy_ms: int
    """Time requests of the route were in flight, summed over concurrent requests."""
    active_minutes: int
    presence_share: float
    """Share of active minutes the route ran in."""
    high_memory_minutes: int
    high_memory_share: float
    """Share of attributed memory peaks the route was in flight for."""
    sole_high_memory_minutes: int
    """Memory peaks the route was the only route in flight for."""
    mem_peak_p95_with_mb: int
    mem_peak_p95_without_mb: int
    """p95 memory peak in active minutes without the route."""
    mem_rise_p95_with_mb: int
    mem_rise_p95_without_mb: int
    high_cpu_minutes: int
    high_cpu_share: float
    cpu_peak_p95_with_millicores: int
    cpu_peak_p95_without_millicores: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        route = self.route

        method = self.method

        requests = self.requests

        busy_ms = self.busy_ms

        active_minutes = self.active_minutes

        presence_share = self.presence_share

        high_memory_minutes = self.high_memory_minutes

        high_memory_share = self.high_memory_share

        sole_high_memory_minutes = self.sole_high_memory_minutes

        mem_peak_p95_with_mb = self.mem_peak_p95_with_mb

        mem_peak_p95_without_mb = self.mem_peak_p95_without_mb

        mem_rise_p95_with_mb = self.mem_rise_p95_with_mb

        mem_rise_p95_without_mb = self.mem_rise_p95_without_mb

        high_cpu_minutes = self.high_cpu_minutes

        high_cpu_share = self.high_cpu_share

        cpu_peak_p95_with_millicores = self.cpu_peak_p95_with_millicores

        cpu_peak_p95_without_millicores = self.cpu_peak_p95_without_millicores

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "route": route,
                "method": method,
                "requests": requests,
                "busy_ms": busy_ms,
                "active_minutes": active_minutes,
                "presence_share": presence_share,
                "high_memory_minutes": high_memory_minutes,
                "high_memory_share": high_memory_share,
                "sole_high_memory_minutes": sole_high_memory_minutes,
                "mem_peak_p95_with_mb": mem_peak_p95_with_mb,
                "mem_peak_p95_without_mb": mem_peak_p95_without_mb,
                "mem_rise_p95_with_mb": mem_rise_p95_with_mb,
                "mem_rise_p95_without_mb": mem_rise_p95_without_mb,
                "high_cpu_minutes": high_cpu_minutes,
                "high_cpu_share": high_cpu_share,
                "cpu_peak_p95_with_millicores": cpu_peak_p95_with_millicores,
                "cpu_peak_p95_without_millicores": cpu_peak_p95_without_millicores,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        route = d.pop("route")

        method = d.pop("method")

        requests = d.pop("requests")

        busy_ms = d.pop("busy_ms")

        active_minutes = d.pop("active_minutes")

        presence_share = d.pop("presence_share")

        high_memory_minutes = d.pop("high_memory_minutes")

        high_memory_share = d.pop("high_memory_share")

        sole_high_memory_minutes = d.pop("sole_high_memory_minutes")

        mem_peak_p95_with_mb = d.pop("mem_peak_p95_with_mb")

        mem_peak_p95_without_mb = d.pop("mem_peak_p95_without_mb")

        mem_rise_p95_with_mb = d.pop("mem_rise_p95_with_mb")

        mem_rise_p95_without_mb = d.pop("mem_rise_p95_without_mb")

        high_cpu_minutes = d.pop("high_cpu_minutes")

        high_cpu_share = d.pop("high_cpu_share")

        cpu_peak_p95_with_millicores = d.pop("cpu_peak_p95_with_millicores")

        cpu_peak_p95_without_millicores = d.pop("cpu_peak_p95_without_millicores")

        route_resource_attribution = cls(
            route=route,
            method=method,
            requests=requests,
            busy_ms=busy_ms,
            active_minutes=active_minutes,
            presence_share=presence_share,
            high_memory_minutes=high_memory_minutes,
            high_memory_share=high_memory_share,
            sole_high_memory_minutes=sole_high_memory_minutes,
            mem_peak_p95_with_mb=mem_peak_p95_with_mb,
            mem_peak_p95_without_mb=mem_peak_p95_without_mb,
            mem_rise_p95_with_mb=mem_rise_p95_with_mb,
            mem_rise_p95_without_mb=mem_rise_p95_without_mb,
            high_cpu_minutes=high_cpu_minutes,
            high_cpu_share=high_cpu_share,
            cpu_peak_p95_with_millicores=cpu_peak_p95_with_millicores,
            cpu_peak_p95_without_millicores=cpu_peak_p95_without_millicores,
        )

        route_resource_attribution.additional_properties = d
        return route_resource_attribution

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
