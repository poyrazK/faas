from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ThrottleSuggestionRow")


@_attrs_define
class ThrottleSuggestionRow:
    """One (route → suggested rate) row in the payload returned by
    `GET /v1/apps/{slug}/throttle-suggestions` (ADR-091 D20.5
    amendment, issue #881). The recommender is read-only — it
    never auto-applies — and the suggestion is always ≤ the
    customer's plan ceiling so a customer can act on it
    without a 422 from apid's sub-plan validator.

    """

    route: str
    """The bounded label exactly as emitted on the Prometheus
    side: `<METHOD> <PATH>` (pre-edge-rule-rewrite), or
    the reserved `__route_other__` overflow bucket label.
    """
    observed_rps: float
    """The `rate()` value over the window (already per-second)."""
    suggested_rps: float
    """`ceil(observed_rps * multiplier)` clamped into
    `[1, plan.RateLimitRPS]`. The 2× headroom is echoed
    on the wire so the value is auditable rather than
    magic.
    """
    suggested_burst: int
    """`ceil(suggested_rps * 1.5)` clamped into
    `[1, plan.RateLimitBurst]`. The 1.5× factor is a
    softer version of the rate headroom — burst oversize
    is the most common cause of customer-flapping 429s.
    """
    multiplier: float
    """The headroom factor the recommender applied to
    `observed_rps`. Pinned on the wire so a future
    strategy change is distinguishable from drift.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        route = self.route

        observed_rps = self.observed_rps

        suggested_rps = self.suggested_rps

        suggested_burst = self.suggested_burst

        multiplier = self.multiplier

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "route": route,
                "observed_rps": observed_rps,
                "suggested_rps": suggested_rps,
                "suggested_burst": suggested_burst,
                "multiplier": multiplier,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        route = d.pop("route")

        observed_rps = d.pop("observed_rps")

        suggested_rps = d.pop("suggested_rps")

        suggested_burst = d.pop("suggested_burst")

        multiplier = d.pop("multiplier")

        throttle_suggestion_row = cls(
            route=route,
            observed_rps=observed_rps,
            suggested_rps=suggested_rps,
            suggested_burst=suggested_burst,
            multiplier=multiplier,
        )

        throttle_suggestion_row.additional_properties = d
        return throttle_suggestion_row

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
