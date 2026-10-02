from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RouteHealthCounts")


@_attrs_define
class RouteHealthCounts:
    requests: int
    server_errors: int
    error_rate: float
    p95_latency_ms: float | Unset = UNSET
    """Weighted p95 estimate in milliseconds from collapsed telemetry representatives. Omitted when latency is not
    selected or evidence is unavailable; zero is a valid observation."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        requests = self.requests

        server_errors = self.server_errors

        error_rate = self.error_rate

        p95_latency_ms = self.p95_latency_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "requests": requests,
                "server_errors": server_errors,
                "error_rate": error_rate,
            }
        )
        if p95_latency_ms is not UNSET:
            field_dict["p95_latency_ms"] = p95_latency_ms

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        requests = d.pop("requests")

        server_errors = d.pop("server_errors")

        error_rate = d.pop("error_rate")

        p95_latency_ms = d.pop("p95_latency_ms", UNSET)

        route_health_counts = cls(
            requests=requests,
            server_errors=server_errors,
            error_rate=error_rate,
            p95_latency_ms=p95_latency_ms,
        )

        route_health_counts.additional_properties = d
        return route_health_counts

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
