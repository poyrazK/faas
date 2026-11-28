from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.request_analytics_group_method import RequestAnalyticsGroupMethod, check_request_analytics_group_method
from ..types import UNSET, Unset

T = TypeVar("T", bound="RequestAnalyticsGroup")


@_attrs_define
class RequestAnalyticsGroup:
    """Bounded aggregate for a selected analytics dimension. Value is a route, country, hostname, normalized client family,
    status code, stable consumer UUID, __anonymous__, or __other__.

    """

    value: str
    requests: int
    error_requests: int
    error_rate_pct: float
    cold_boots: int
    p50_ms: int
    p95_ms: int
    p99_ms: int
    method: RequestAnalyticsGroupMethod | Unset = UNSET
    cold_request_p95_ms: int | None | Unset = UNSET
    wake_boot_p95_ms: int | None | Unset = UNSET
    guest_execution_p50_ms: int | None | Unset = UNSET
    guest_execution_p95_ms: int | None | Unset = UNSET
    guest_cpu_avg_ms: int | None | Unset = UNSET
    guest_cpu_p95_ms: int | None | Unset = UNSET
    guest_peak_rss_max_mb: int | None | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        value = self.value

        requests = self.requests

        error_requests = self.error_requests

        error_rate_pct = self.error_rate_pct

        cold_boots = self.cold_boots

        p50_ms = self.p50_ms

        p95_ms = self.p95_ms

        p99_ms = self.p99_ms

        method: str | Unset = UNSET
        if not isinstance(self.method, Unset):
            method = self.method

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

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "value": value,
                "requests": requests,
                "error_requests": error_requests,
                "error_rate_pct": error_rate_pct,
                "cold_boots": cold_boots,
                "p50_ms": p50_ms,
                "p95_ms": p95_ms,
                "p99_ms": p99_ms,
            }
        )
        if method is not UNSET:
            field_dict["method"] = method
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

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        value = d.pop("value")

        requests = d.pop("requests")

        error_requests = d.pop("error_requests")

        error_rate_pct = d.pop("error_rate_pct")

        cold_boots = d.pop("cold_boots")

        p50_ms = d.pop("p50_ms")

        p95_ms = d.pop("p95_ms")

        p99_ms = d.pop("p99_ms")

        _method = d.pop("method", UNSET)
        method: RequestAnalyticsGroupMethod | Unset
        if isinstance(_method, Unset):
            method = UNSET
        else:
            method = check_request_analytics_group_method(_method)

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

        request_analytics_group = cls(
            value=value,
            requests=requests,
            error_requests=error_requests,
            error_rate_pct=error_rate_pct,
            cold_boots=cold_boots,
            p50_ms=p50_ms,
            p95_ms=p95_ms,
            p99_ms=p99_ms,
            method=method,
            cold_request_p95_ms=cold_request_p95_ms,
            wake_boot_p95_ms=wake_boot_p95_ms,
            guest_execution_p50_ms=guest_execution_p50_ms,
            guest_execution_p95_ms=guest_execution_p95_ms,
            guest_cpu_avg_ms=guest_cpu_avg_ms,
            guest_cpu_p95_ms=guest_cpu_p95_ms,
            guest_peak_rss_max_mb=guest_peak_rss_max_mb,
        )

        request_analytics_group.additional_properties = d
        return request_analytics_group

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
