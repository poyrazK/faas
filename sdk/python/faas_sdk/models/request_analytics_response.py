from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.request_analytics_route import RequestAnalyticsRoute


T = TypeVar("T", bound="RequestAnalyticsResponse")


@_attrs_define
class RequestAnalyticsResponse:
    """Bounded historical request analytics for
    `GET /v1/apps/{slug}/analytics?since=`. The window is half-open
    `[now-since, now)`, and the route list is capped at 50 rows.

    """

    slug: str
    since: str
    """Effective lookback duration after retention clamping."""
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
    routes: list[RequestAnalyticsRoute]
    routes_limit: int
    """Maximum number of route rows returned."""
    routes_truncated: bool
    """True when more route rows matched than routes_limit."""
    as_of: datetime.datetime
    """RFC3339Nano UTC assembly timestamp."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        slug = self.slug

        since = self.since

        until = self.until.isoformat()

        window_clamped = self.window_clamped

        requests = self.requests

        error_requests = self.error_requests

        error_rate_pct = self.error_rate_pct

        cold_boots = self.cold_boots

        p50_ms = self.p50_ms

        p95_ms = self.p95_ms

        p99_ms = self.p99_ms

        routes = []
        for routes_item_data in self.routes:
            routes_item = routes_item_data.to_dict()
            routes.append(routes_item)

        routes_limit = self.routes_limit

        routes_truncated = self.routes_truncated

        as_of = self.as_of.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "slug": slug,
                "since": since,
                "until": until,
                "window_clamped": window_clamped,
                "requests": requests,
                "error_requests": error_requests,
                "error_rate_pct": error_rate_pct,
                "cold_boots": cold_boots,
                "p50_ms": p50_ms,
                "p95_ms": p95_ms,
                "p99_ms": p99_ms,
                "routes": routes,
                "routes_limit": routes_limit,
                "routes_truncated": routes_truncated,
                "as_of": as_of,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.request_analytics_route import RequestAnalyticsRoute

        d = dict(src_dict)
        slug = d.pop("slug")

        since = d.pop("since")

        until = datetime.datetime.fromisoformat(d.pop("until"))

        window_clamped = d.pop("window_clamped")

        requests = d.pop("requests")

        error_requests = d.pop("error_requests")

        error_rate_pct = d.pop("error_rate_pct")

        cold_boots = d.pop("cold_boots")

        p50_ms = d.pop("p50_ms")

        p95_ms = d.pop("p95_ms")

        p99_ms = d.pop("p99_ms")

        routes = []
        _routes = d.pop("routes")
        for routes_item_data in _routes:
            routes_item = RequestAnalyticsRoute.from_dict(routes_item_data)

            routes.append(routes_item)

        routes_limit = d.pop("routes_limit")

        routes_truncated = d.pop("routes_truncated")

        as_of = datetime.datetime.fromisoformat(d.pop("as_of"))

        request_analytics_response = cls(
            slug=slug,
            since=since,
            until=until,
            window_clamped=window_clamped,
            requests=requests,
            error_requests=error_requests,
            error_rate_pct=error_rate_pct,
            cold_boots=cold_boots,
            p50_ms=p50_ms,
            p95_ms=p95_ms,
            p99_ms=p99_ms,
            routes=routes,
            routes_limit=routes_limit,
            routes_truncated=routes_truncated,
            as_of=as_of,
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
