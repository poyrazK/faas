from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="OutboundRequestPolicy")


@_attrs_define
class OutboundRequestPolicy:
    """Effective customer-selected per-integration policy, bounded by the account plan."""

    rate_per_second: float
    burst: int
    max_in_flight: int
    request_timeout_ms: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        rate_per_second = self.rate_per_second

        burst = self.burst

        max_in_flight = self.max_in_flight

        request_timeout_ms = self.request_timeout_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "rate_per_second": rate_per_second,
                "burst": burst,
                "max_in_flight": max_in_flight,
                "request_timeout_ms": request_timeout_ms,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        rate_per_second = d.pop("rate_per_second")

        burst = d.pop("burst")

        max_in_flight = d.pop("max_in_flight")

        request_timeout_ms = d.pop("request_timeout_ms")

        outbound_request_policy = cls(
            rate_per_second=rate_per_second,
            burst=burst,
            max_in_flight=max_in_flight,
            request_timeout_ms=request_timeout_ms,
        )

        outbound_request_policy.additional_properties = d
        return outbound_request_policy

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
