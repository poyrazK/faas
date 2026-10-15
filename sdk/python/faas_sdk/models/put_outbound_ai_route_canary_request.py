from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="PutOutboundAIRouteCanaryRequest")


@_attrs_define
class PutOutboundAIRouteCanaryRequest:
    """Start or replace a route's model canary. Omitted margins take their defaults: 5 error-rate points, 50 calls, 30
    minutes, and no latency margin.

    """

    target: int
    """The 1-based route position tried first."""
    percent: int
    max_error_rate_increase: int | Unset = UNSET
    max_latency_increase_percent: int | Unset = UNSET
    min_calls: int | Unset = UNSET
    window_minutes: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        target = self.target

        percent = self.percent

        max_error_rate_increase = self.max_error_rate_increase

        max_latency_increase_percent = self.max_latency_increase_percent

        min_calls = self.min_calls

        window_minutes = self.window_minutes

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "target": target,
                "percent": percent,
            }
        )
        if max_error_rate_increase is not UNSET:
            field_dict["max_error_rate_increase"] = max_error_rate_increase
        if max_latency_increase_percent is not UNSET:
            field_dict["max_latency_increase_percent"] = max_latency_increase_percent
        if min_calls is not UNSET:
            field_dict["min_calls"] = min_calls
        if window_minutes is not UNSET:
            field_dict["window_minutes"] = window_minutes

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        target = d.pop("target")

        percent = d.pop("percent")

        max_error_rate_increase = d.pop("max_error_rate_increase", UNSET)

        max_latency_increase_percent = d.pop("max_latency_increase_percent", UNSET)

        min_calls = d.pop("min_calls", UNSET)

        window_minutes = d.pop("window_minutes", UNSET)

        put_outbound_ai_route_canary_request = cls(
            target=target,
            percent=percent,
            max_error_rate_increase=max_error_rate_increase,
            max_latency_increase_percent=max_latency_increase_percent,
            min_calls=min_calls,
            window_minutes=window_minutes,
        )

        put_outbound_ai_route_canary_request.additional_properties = d
        return put_outbound_ai_route_canary_request

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
