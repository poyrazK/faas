from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.request_analytics_route_method import RequestAnalyticsRouteMethod, check_request_analytics_route_method
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.request_analytics_dependency import RequestAnalyticsDependency


T = TypeVar("T", bound="RequestAnalyticsRoute")


@_attrs_define
class RequestAnalyticsRoute:
    """Aggregated request analytics for one route and HTTP method. Counts include collapsed telemetry row weights."""

    route: str
    """Route template, not an expanded URL."""
    method: RequestAnalyticsRouteMethod
    requests: int
    error_requests: int
    error_rate_pct: float
    cold_boots: int
    p50_ms: int
    p95_ms: int
    p99_ms: int
    cold_request_p95_ms: int | None | Unset = UNSET
    """Weighted p95 gateway-observed request duration among requests that woke a cold instance. Includes handler
    time; not an isolated wake-phase duration."""
    wake_boot_p95_ms: int | None | Unset = UNSET
    """p95 from schedd wake.boot_started to wake.boot_completed (instance RUNNING) for distinct wake IDs correlated
    to this route; null when event pairs are unavailable."""
    guest_execution_p50_ms: int | None | Unset = UNSET
    """Weighted p50 wall time from platform-runner execution evidence; not CPU time. Null when runtime evidence is
    unavailable."""
    guest_execution_p95_ms: int | None | Unset = UNSET
    """Weighted p95 wall time from platform-runner execution evidence; not CPU time. Null when runtime evidence is
    unavailable."""
    guest_cpu_avg_ms: int | None | Unset = UNSET
    """Request-weighted average child-process user+system CPU time for Linux one-shot invocations. Includes runtime
    startup and uses bounded upward buckets; null for persistent workers and arbitrary HTTP containers."""
    guest_cpu_p95_ms: int | None | Unset = UNSET
    """Weighted p95 child-process CPU time from Linux one-shot invocations, using bounded upward buckets; null when
    process usage is unavailable."""
    guest_peak_rss_max_mb: int | None | Unset = UNSET
    """Maximum process high-water RSS across measured invocations, rounded into an upper MiB bucket; null when
    process usage is unavailable."""
    dependency_samples: int | Unset = UNSET
    """Count of retained, platform-classified dependency span samples for this route; not a complete call count."""
    dependency_requests: int | Unset = UNSET
    """Collapsed request weight represented by rows with at least one classified dependency span."""
    dependencies: list[RequestAnalyticsDependency] | Unset = UNSET
    """Top classified dependencies ranked by sampled exclusive p95. Values are bounded and sample-based."""
    estimated_compute_cost_millicents: int | Unset = UNSET
    """Estimated raw RAM-hour value allocated to this route by observed request share; excludes account-level
    included allowance and egress."""
    request_share_pct: float | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        route = self.route

        method: str = self.method

        requests = self.requests

        error_requests = self.error_requests

        error_rate_pct = self.error_rate_pct

        cold_boots = self.cold_boots

        p50_ms = self.p50_ms

        p95_ms = self.p95_ms

        p99_ms = self.p99_ms

        cold_request_p95_ms: int | None | Unset
        if isinstance(self.cold_request_p95_ms, Unset):
            cold_request_p95_ms = UNSET
        else:
            cold_request_p95_ms = self.cold_request_p95_ms

        wake_boot_p95_ms: int | None | Unset
        if isinstance(self.wake_boot_p95_ms, Unset):
            wake_boot_p95_ms = UNSET
        else:
            wake_boot_p95_ms = self.wake_boot_p95_ms

        guest_execution_p50_ms: int | None | Unset
        if isinstance(self.guest_execution_p50_ms, Unset):
            guest_execution_p50_ms = UNSET
        else:
            guest_execution_p50_ms = self.guest_execution_p50_ms

        guest_execution_p95_ms: int | None | Unset
        if isinstance(self.guest_execution_p95_ms, Unset):
            guest_execution_p95_ms = UNSET
        else:
            guest_execution_p95_ms = self.guest_execution_p95_ms

        guest_cpu_avg_ms: int | None | Unset
        if isinstance(self.guest_cpu_avg_ms, Unset):
            guest_cpu_avg_ms = UNSET
        else:
            guest_cpu_avg_ms = self.guest_cpu_avg_ms

        guest_cpu_p95_ms: int | None | Unset
        if isinstance(self.guest_cpu_p95_ms, Unset):
            guest_cpu_p95_ms = UNSET
        else:
            guest_cpu_p95_ms = self.guest_cpu_p95_ms

        guest_peak_rss_max_mb: int | None | Unset
        if isinstance(self.guest_peak_rss_max_mb, Unset):
            guest_peak_rss_max_mb = UNSET
        else:
            guest_peak_rss_max_mb = self.guest_peak_rss_max_mb

        dependency_samples = self.dependency_samples

        dependency_requests = self.dependency_requests

        dependencies: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.dependencies, Unset):
            dependencies = []
            for dependencies_item_data in self.dependencies:
                dependencies_item = dependencies_item_data.to_dict()
                dependencies.append(dependencies_item)

        estimated_compute_cost_millicents = self.estimated_compute_cost_millicents

        request_share_pct = self.request_share_pct

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "route": route,
                "method": method,
                "requests": requests,
                "error_requests": error_requests,
                "error_rate_pct": error_rate_pct,
                "cold_boots": cold_boots,
                "p50_ms": p50_ms,
                "p95_ms": p95_ms,
                "p99_ms": p99_ms,
            }
        )
        if cold_request_p95_ms is not UNSET:
            field_dict["cold_request_p95_ms"] = cold_request_p95_ms
        if wake_boot_p95_ms is not UNSET:
            field_dict["wake_boot_p95_ms"] = wake_boot_p95_ms
        if guest_execution_p50_ms is not UNSET:
            field_dict["guest_execution_p50_ms"] = guest_execution_p50_ms
        if guest_execution_p95_ms is not UNSET:
            field_dict["guest_execution_p95_ms"] = guest_execution_p95_ms
        if guest_cpu_avg_ms is not UNSET:
            field_dict["guest_cpu_avg_ms"] = guest_cpu_avg_ms
        if guest_cpu_p95_ms is not UNSET:
            field_dict["guest_cpu_p95_ms"] = guest_cpu_p95_ms
        if guest_peak_rss_max_mb is not UNSET:
            field_dict["guest_peak_rss_max_mb"] = guest_peak_rss_max_mb
        if dependency_samples is not UNSET:
            field_dict["dependency_samples"] = dependency_samples
        if dependency_requests is not UNSET:
            field_dict["dependency_requests"] = dependency_requests
        if dependencies is not UNSET:
            field_dict["dependencies"] = dependencies
        if estimated_compute_cost_millicents is not UNSET:
            field_dict["estimated_compute_cost_millicents"] = estimated_compute_cost_millicents
        if request_share_pct is not UNSET:
            field_dict["request_share_pct"] = request_share_pct

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.request_analytics_dependency import RequestAnalyticsDependency

        d = dict(src_dict)
        route = d.pop("route")

        method = check_request_analytics_route_method(d.pop("method"))

        requests = d.pop("requests")

        error_requests = d.pop("error_requests")

        error_rate_pct = d.pop("error_rate_pct")

        cold_boots = d.pop("cold_boots")

        p50_ms = d.pop("p50_ms")

        p95_ms = d.pop("p95_ms")

        p99_ms = d.pop("p99_ms")

        def _parse_cold_request_p95_ms(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        cold_request_p95_ms = _parse_cold_request_p95_ms(d.pop("cold_request_p95_ms", UNSET))

        def _parse_wake_boot_p95_ms(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        wake_boot_p95_ms = _parse_wake_boot_p95_ms(d.pop("wake_boot_p95_ms", UNSET))

        def _parse_guest_execution_p50_ms(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        guest_execution_p50_ms = _parse_guest_execution_p50_ms(d.pop("guest_execution_p50_ms", UNSET))

        def _parse_guest_execution_p95_ms(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        guest_execution_p95_ms = _parse_guest_execution_p95_ms(d.pop("guest_execution_p95_ms", UNSET))

        def _parse_guest_cpu_avg_ms(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        guest_cpu_avg_ms = _parse_guest_cpu_avg_ms(d.pop("guest_cpu_avg_ms", UNSET))

        def _parse_guest_cpu_p95_ms(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        guest_cpu_p95_ms = _parse_guest_cpu_p95_ms(d.pop("guest_cpu_p95_ms", UNSET))

        def _parse_guest_peak_rss_max_mb(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        guest_peak_rss_max_mb = _parse_guest_peak_rss_max_mb(d.pop("guest_peak_rss_max_mb", UNSET))

        dependency_samples = d.pop("dependency_samples", UNSET)

        dependency_requests = d.pop("dependency_requests", UNSET)

        _dependencies = d.pop("dependencies", UNSET)
        dependencies: list[RequestAnalyticsDependency] | Unset = UNSET
        if _dependencies is not UNSET:
            dependencies = []
            for dependencies_item_data in _dependencies:
                dependencies_item = RequestAnalyticsDependency.from_dict(dependencies_item_data)

                dependencies.append(dependencies_item)

        estimated_compute_cost_millicents = d.pop("estimated_compute_cost_millicents", UNSET)

        request_share_pct = d.pop("request_share_pct", UNSET)

        request_analytics_route = cls(
            route=route,
            method=method,
            requests=requests,
            error_requests=error_requests,
            error_rate_pct=error_rate_pct,
            cold_boots=cold_boots,
            p50_ms=p50_ms,
            p95_ms=p95_ms,
            p99_ms=p99_ms,
            cold_request_p95_ms=cold_request_p95_ms,
            wake_boot_p95_ms=wake_boot_p95_ms,
            guest_execution_p50_ms=guest_execution_p50_ms,
            guest_execution_p95_ms=guest_execution_p95_ms,
            guest_cpu_avg_ms=guest_cpu_avg_ms,
            guest_cpu_p95_ms=guest_cpu_p95_ms,
            guest_peak_rss_max_mb=guest_peak_rss_max_mb,
            dependency_samples=dependency_samples,
            dependency_requests=dependency_requests,
            dependencies=dependencies,
            estimated_compute_cost_millicents=estimated_compute_cost_millicents,
            request_share_pct=request_share_pct,
        )

        request_analytics_route.additional_properties = d
        return request_analytics_route

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
