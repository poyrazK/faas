from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.outbound_ai_route_canary import OutboundAIRouteCanary
    from ..models.outbound_ai_route_target_stats import OutboundAIRouteTargetStats


T = TypeVar("T", bound="OutboundAIRouteCanaryStatus")


@_attrs_define
class OutboundAIRouteCanaryStatus:
    """A canary with the route's first target's and its own counts since window_start."""

    canary: OutboundAIRouteCanary
    """A route's model canary (ADR-1340). target is the 1-based route position tried first by percent of the
    route's calls, chosen per X-Gregale-AI-Session, else X-Gregale-AI-User, else at random. outboundd rolls the
    canary back when, after min_calls canary attempts in the window, its error rate exceeds the first target's by
    more than max_error_rate_increase percentage points, or its mean time to first byte by more than
    max_latency_increase_percent."""
    window_start: datetime.datetime
    baseline: OutboundAIRouteTargetStats
    """One target's settled attempts in a canary's or a strategy's window. Errors are attempts with a provider
    status of 400 or above; mean_ttfb_ms averages successful attempts that received a first byte, and
    mean_cost_millicents is the cost per successful attempt; each is null without one."""
    canary_stats: OutboundAIRouteTargetStats
    """One target's settled attempts in a canary's or a strategy's window. Errors are attempts with a provider
    status of 400 or above; mean_ttfb_ms averages successful attempts that received a first byte, and
    mean_cost_millicents is the cost per successful attempt; each is null without one."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        canary = self.canary.to_dict()

        window_start = self.window_start.isoformat()

        baseline = self.baseline.to_dict()

        canary_stats = self.canary_stats.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "canary": canary,
                "window_start": window_start,
                "baseline": baseline,
                "canary_stats": canary_stats,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.outbound_ai_route_canary import OutboundAIRouteCanary
        from ..models.outbound_ai_route_target_stats import OutboundAIRouteTargetStats

        d = dict(src_dict)
        canary = OutboundAIRouteCanary.from_dict(d.pop("canary"))

        window_start = datetime.datetime.fromisoformat(d.pop("window_start"))

        baseline = OutboundAIRouteTargetStats.from_dict(d.pop("baseline"))

        canary_stats = OutboundAIRouteTargetStats.from_dict(d.pop("canary_stats"))

        outbound_ai_route_canary_status = cls(
            canary=canary,
            window_start=window_start,
            baseline=baseline,
            canary_stats=canary_stats,
        )

        outbound_ai_route_canary_status.additional_properties = d
        return outbound_ai_route_canary_status

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
