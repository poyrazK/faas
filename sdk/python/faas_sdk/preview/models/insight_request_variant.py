from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="InsightRequestVariant")


@_attrs_define
class InsightRequestVariant:
    """Request telemetry for one value of a flag."""

    value: str
    requests: int
    server_errors: int
    server_error_rate: float
    p95_ms: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        value = self.value

        requests = self.requests

        server_errors = self.server_errors

        server_error_rate = self.server_error_rate

        p95_ms = self.p95_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "value": value,
                "requests": requests,
                "server_errors": server_errors,
                "server_error_rate": server_error_rate,
                "p95_ms": p95_ms,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        value = d.pop("value")

        requests = d.pop("requests")

        server_errors = d.pop("server_errors")

        server_error_rate = d.pop("server_error_rate")

        p95_ms = d.pop("p95_ms")

        insight_request_variant = cls(
            value=value,
            requests=requests,
            server_errors=server_errors,
            server_error_rate=server_error_rate,
            p95_ms=p95_ms,
        )

        insight_request_variant.additional_properties = d
        return insight_request_variant

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
