from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RouteAdviceImpact")


@_attrs_define
class RouteAdviceImpact:
    """What-if estimate replayed from aggregated telemetry over the same window; observed_only, not a guarantee."""

    summary: str
    """One-sentence what-if estimate for the suggested rule."""
    estimated_cache_hits: int
    estimated_wakes_avoided: int
    estimated_throttled_requests: int
    timeouts_affected: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        summary = self.summary

        estimated_cache_hits = self.estimated_cache_hits

        estimated_wakes_avoided = self.estimated_wakes_avoided

        estimated_throttled_requests = self.estimated_throttled_requests

        timeouts_affected = self.timeouts_affected

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "summary": summary,
                "estimated_cache_hits": estimated_cache_hits,
                "estimated_wakes_avoided": estimated_wakes_avoided,
                "estimated_throttled_requests": estimated_throttled_requests,
                "timeouts_affected": timeouts_affected,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        summary = d.pop("summary")

        estimated_cache_hits = d.pop("estimated_cache_hits")

        estimated_wakes_avoided = d.pop("estimated_wakes_avoided")

        estimated_throttled_requests = d.pop("estimated_throttled_requests")

        timeouts_affected = d.pop("timeouts_affected")

        route_advice_impact = cls(
            summary=summary,
            estimated_cache_hits=estimated_cache_hits,
            estimated_wakes_avoided=estimated_wakes_avoided,
            estimated_throttled_requests=estimated_throttled_requests,
            timeouts_affected=timeouts_affected,
        )

        route_advice_impact.additional_properties = d
        return route_advice_impact

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
