from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.request_analytics_response_group_by import (
    RequestAnalyticsResponseGroupBy,
    check_request_analytics_response_group_by,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.request_analytics_compute_cost import RequestAnalyticsComputeCost
    from ..models.request_analytics_deployment_cost_breakdown import RequestAnalyticsDeploymentCostBreakdown
    from ..models.request_analytics_group import RequestAnalyticsGroup
    from ..models.request_analytics_route import RequestAnalyticsRoute


T = TypeVar("T", bound="RequestAnalyticsResponse")


@_attrs_define
class RequestAnalyticsResponse:
    """Bounded historical request analytics for
    `GET /v1/apps/{slug}/analytics?since=`. The window is half-open
    `[from, until)`, and the route list is capped at 50 rows.

    """

    slug: str
    since: str
    """Effective lookback duration after retention clamping."""
    from_: datetime.datetime
    """Inclusive lower bound of the analytics window."""
    until: datetime.datetime
    """Exclusive upper bound of the analytics window."""
    window_clamped: bool
    """True when the requested lookback exceeded plan retention."""
    requests: int
    error_requests: int
    error_rate_pct: float
    cold_boots: int
    p50_ms: int
    p95_ms: int
    p99_ms: int
    group_by: RequestAnalyticsResponseGroupBy
    groups: list[RequestAnalyticsGroup]
    groups_limit: int
    """Maximum number of top groups before __other__."""
    groups_truncated: bool
    """True when __other__ contains groups outside the top-N."""
    routes: list[RequestAnalyticsRoute]
    routes_limit: int
    """Maximum number of route rows returned."""
    routes_truncated: bool
    """True when more route rows matched than routes_limit."""
    dependencies_truncated: bool
    """True when dependency span rows, route dependency groups, or route dependency outputs were capped."""
    as_of: datetime.datetime
    """RFC3339Nano UTC assembly timestamp."""
    compute_cost: RequestAnalyticsComputeCost | Unset = UNSET
    """Estimated app compute value over the analytics window, allocated by observed request share. This is not an
    invoice amount; it values raw RAM-hours before the account's included allowance and excludes egress."""
    deployment_costs: RequestAnalyticsDeploymentCostBreakdown | Unset = UNSET
    """Bounded deployment allocation of the app's estimated raw compute value for the analytics window. It is not
    an invoice amount."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        slug = self.slug

        since = self.since

        from_ = self.from_.isoformat()

        until = self.until.isoformat()

        window_clamped = self.window_clamped

        requests = self.requests

        error_requests = self.error_requests

        error_rate_pct = self.error_rate_pct

        cold_boots = self.cold_boots

        p50_ms = self.p50_ms

        p95_ms = self.p95_ms

        p99_ms = self.p99_ms

        group_by: str = self.group_by

        groups = []
        for groups_item_data in self.groups:
            groups_item = groups_item_data.to_dict()
            groups.append(groups_item)

        groups_limit = self.groups_limit

        groups_truncated = self.groups_truncated

        routes = []
        for routes_item_data in self.routes:
            routes_item = routes_item_data.to_dict()
            routes.append(routes_item)

        routes_limit = self.routes_limit

        routes_truncated = self.routes_truncated

        dependencies_truncated = self.dependencies_truncated

        as_of = self.as_of.isoformat()

        compute_cost: dict[str, Any] | Unset = UNSET
        if not isinstance(self.compute_cost, Unset):
            compute_cost = self.compute_cost.to_dict()

        deployment_costs: dict[str, Any] | Unset = UNSET
        if not isinstance(self.deployment_costs, Unset):
            deployment_costs = self.deployment_costs.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "slug": slug,
                "since": since,
                "from": from_,
                "until": until,
                "window_clamped": window_clamped,
                "requests": requests,
                "error_requests": error_requests,
                "error_rate_pct": error_rate_pct,
                "cold_boots": cold_boots,
                "p50_ms": p50_ms,
                "p95_ms": p95_ms,
                "p99_ms": p99_ms,
                "group_by": group_by,
                "groups": groups,
                "groups_limit": groups_limit,
                "groups_truncated": groups_truncated,
                "routes": routes,
                "routes_limit": routes_limit,
                "routes_truncated": routes_truncated,
                "dependencies_truncated": dependencies_truncated,
                "as_of": as_of,
            }
        )
        if compute_cost is not UNSET:
            field_dict["compute_cost"] = compute_cost
        if deployment_costs is not UNSET:
            field_dict["deployment_costs"] = deployment_costs

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.request_analytics_compute_cost import RequestAnalyticsComputeCost
        from ..models.request_analytics_deployment_cost_breakdown import RequestAnalyticsDeploymentCostBreakdown
        from ..models.request_analytics_group import RequestAnalyticsGroup
        from ..models.request_analytics_route import RequestAnalyticsRoute

        d = dict(src_dict)
        slug = d.pop("slug")

        since = d.pop("since")

        from_ = datetime.datetime.fromisoformat(d.pop("from"))

        until = datetime.datetime.fromisoformat(d.pop("until"))

        window_clamped = d.pop("window_clamped")

        requests = d.pop("requests")

        error_requests = d.pop("error_requests")

        error_rate_pct = d.pop("error_rate_pct")

        cold_boots = d.pop("cold_boots")

        p50_ms = d.pop("p50_ms")

        p95_ms = d.pop("p95_ms")

        p99_ms = d.pop("p99_ms")

        group_by = check_request_analytics_response_group_by(d.pop("group_by"))

        groups = []
        _groups = d.pop("groups")
        for groups_item_data in _groups:
            groups_item = RequestAnalyticsGroup.from_dict(groups_item_data)

            groups.append(groups_item)

        groups_limit = d.pop("groups_limit")

        groups_truncated = d.pop("groups_truncated")

        routes = []
        _routes = d.pop("routes")
        for routes_item_data in _routes:
            routes_item = RequestAnalyticsRoute.from_dict(routes_item_data)

            routes.append(routes_item)

        routes_limit = d.pop("routes_limit")

        routes_truncated = d.pop("routes_truncated")

        dependencies_truncated = d.pop("dependencies_truncated")

        as_of = datetime.datetime.fromisoformat(d.pop("as_of"))

        _compute_cost = d.pop("compute_cost", UNSET)
        compute_cost: RequestAnalyticsComputeCost | Unset
        if isinstance(_compute_cost, Unset):
            compute_cost = UNSET
        else:
            compute_cost = RequestAnalyticsComputeCost.from_dict(_compute_cost)

        _deployment_costs = d.pop("deployment_costs", UNSET)
        deployment_costs: RequestAnalyticsDeploymentCostBreakdown | Unset
        if isinstance(_deployment_costs, Unset):
            deployment_costs = UNSET
        else:
            deployment_costs = RequestAnalyticsDeploymentCostBreakdown.from_dict(_deployment_costs)

        request_analytics_response = cls(
            slug=slug,
            since=since,
            from_=from_,
            until=until,
            window_clamped=window_clamped,
            requests=requests,
            error_requests=error_requests,
            error_rate_pct=error_rate_pct,
            cold_boots=cold_boots,
            p50_ms=p50_ms,
            p95_ms=p95_ms,
            p99_ms=p99_ms,
            group_by=group_by,
            groups=groups,
            groups_limit=groups_limit,
            groups_truncated=groups_truncated,
            routes=routes,
            routes_limit=routes_limit,
            routes_truncated=routes_truncated,
            dependencies_truncated=dependencies_truncated,
            as_of=as_of,
            compute_cost=compute_cost,
            deployment_costs=deployment_costs,
        )

        request_analytics_response.additional_properties = d
        return request_analytics_response

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
