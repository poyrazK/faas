from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.request_analytics_group_method import RequestAnalyticsGroupMethod, check_request_analytics_group_method
from ..types import UNSET, Unset

T = TypeVar("T", bound="RequestAnalyticsGroup")


@_attrs_define
class RequestAnalyticsGroup:
    """Bounded aggregate for a selected analytics dimension. Value is a route, country, hostname, normalized client family,
    status code, or __other__.

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
