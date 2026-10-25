from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_insight_fix_response_cache_purge import (
    RouteInsightFixResponseCachePurge,
    check_route_insight_fix_response_cache_purge,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.edge_rule_response import EdgeRuleResponse
    from ..models.route_insight import RouteInsight
    from ..models.route_insight_fix import RouteInsightFix


T = TypeVar("T", bound="RouteInsightFixResponse")


@_attrs_define
class RouteInsightFixResponse:
    """An applied fix: the insight after it, the fix, the edge rule created or changed, the cache purge outcome, and how
    many of the route's insights it resolved.

    """

    insight: RouteInsight
    """One finding for a route; also the payload of the routes.insight.detected and routes.insight.resolved
    webhooks."""
    fix: RouteInsightFix
    """The edge-rule change applying an active insight would make now, planned from its evidence and the app's
    current rules (ADR-20261020161310). Absent when the platform has no fix; never in webhook payloads."""
    rule: EdgeRuleResponse
    """A customer-configurable edge rule. The `action` blob is a
    kind-tagged union — the shape varies by `kind`. See
    CreateEdgeRuleRequest.action for the per-kind shape.
    """
    resolved: int
    cache_purge: RouteInsightFixResponseCachePurge | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        insight = self.insight.to_dict()

        fix = self.fix.to_dict()

        rule = self.rule.to_dict()

        resolved = self.resolved

        cache_purge: str | Unset = UNSET
        if not isinstance(self.cache_purge, Unset):
            cache_purge = self.cache_purge

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "insight": insight,
                "fix": fix,
                "rule": rule,
                "resolved": resolved,
            }
        )
        if cache_purge is not UNSET:
            field_dict["cache_purge"] = cache_purge

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.edge_rule_response import EdgeRuleResponse
        from ..models.route_insight import RouteInsight
        from ..models.route_insight_fix import RouteInsightFix

        d = dict(src_dict)
        insight = RouteInsight.from_dict(d.pop("insight"))

        fix = RouteInsightFix.from_dict(d.pop("fix"))

        rule = EdgeRuleResponse.from_dict(d.pop("rule"))

        resolved = d.pop("resolved")

        _cache_purge = d.pop("cache_purge", UNSET)
        cache_purge: RouteInsightFixResponseCachePurge | Unset
        if isinstance(_cache_purge, Unset):
            cache_purge = UNSET
        else:
            cache_purge = check_route_insight_fix_response_cache_purge(_cache_purge)

        route_insight_fix_response = cls(
            insight=insight,
            fix=fix,
            rule=rule,
            resolved=resolved,
            cache_purge=cache_purge,
        )

        route_insight_fix_response.additional_properties = d
        return route_insight_fix_response

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
