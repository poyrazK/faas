from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="OutboundAIRouteTargetStats")


@_attrs_define
class OutboundAIRouteTargetStats:
    """One target's settled attempts in a canary's or a strategy's window. Errors are attempts with a provider status of
    400 or above; mean_ttfb_ms averages successful attempts that received a first byte, and mean_cost_millicents is the
    cost per successful attempt; each is null without one.

    """

    target: int
    calls: int
    errors: int
    error_rate: float
    mean_ttfb_ms: int | None
    cost_millicents: int
    mean_cost_millicents: int | None
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        target = self.target

        calls = self.calls

        errors = self.errors

        error_rate = self.error_rate

        mean_ttfb_ms: int | None
        mean_ttfb_ms = self.mean_ttfb_ms

        cost_millicents = self.cost_millicents

        mean_cost_millicents: int | None
        mean_cost_millicents = self.mean_cost_millicents

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "target": target,
                "calls": calls,
                "errors": errors,
                "error_rate": error_rate,
                "mean_ttfb_ms": mean_ttfb_ms,
                "cost_millicents": cost_millicents,
                "mean_cost_millicents": mean_cost_millicents,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        target = d.pop("target")

        calls = d.pop("calls")

        errors = d.pop("errors")

        error_rate = d.pop("error_rate")

        def _parse_mean_ttfb_ms(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        mean_ttfb_ms = _parse_mean_ttfb_ms(d.pop("mean_ttfb_ms"))

        cost_millicents = d.pop("cost_millicents")

        def _parse_mean_cost_millicents(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        mean_cost_millicents = _parse_mean_cost_millicents(d.pop("mean_cost_millicents"))

        outbound_ai_route_target_stats = cls(
            target=target,
            calls=calls,
            errors=errors,
            error_rate=error_rate,
            mean_ttfb_ms=mean_ttfb_ms,
            cost_millicents=cost_millicents,
            mean_cost_millicents=mean_cost_millicents,
        )

        outbound_ai_route_target_stats.additional_properties = d
        return outbound_ai_route_target_stats

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
