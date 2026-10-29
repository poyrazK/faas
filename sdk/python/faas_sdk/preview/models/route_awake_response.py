from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_awake_route import RouteAwakeRoute
    from ..models.route_awake_source import RouteAwakeSource


T = TypeVar("T", bound="RouteAwakeResponse")


@_attrs_define
class RouteAwakeResponse:
    """How long the app was awake in the window and which routes kept it awake. Seconds are instance-seconds of billed
    running time.

    """

    slug: str
    from_: datetime.datetime
    until: datetime.datetime
    idle_timeout_seconds: int
    """The app's effective idle timeout: how long it stays awake after its last successful request."""
    window_seconds: int
    awake_seconds: int
    awake_share: float
    """Share of the window's minutes with at least one instance running."""
    mb_seconds: int
    """Billed MB-seconds in the window."""
    gb_hours: float
    wakes: int
    """Cold boots in the window."""
    unattributed_seconds: int
    """Awake time no recorded request, probe, prewarm or delivery explains: minimum instances, probes older than a
    day, deliveries already pruned by result retention, or telemetry the plan's rate cap dropped."""
    sources: list[RouteAwakeSource]
    """Awake time credited to causes other than customer requests, for minutes no request explains: route probes
    and prewarms, and cron, queue and other deliveries the scheduler made to the app, per cron job and per queue.
   """
    min_instances: int
    """The app's current minimum instance count; above zero the app never parks, so unattributed time is expected."""
    other_routes_seconds: int
    """Awake time attributed to routes outside the busiest routes_limit."""
    routes_limit: int
    routes: list[RouteAwakeRoute]
    window_clamped: bool | Unset = UNSET
    wait_until_timeout_seconds: int | Unset = UNSET
    """The plan's waitUntil task timeout when the plan enables waitUntil. An instance parks no earlier than the
    later of the idle timeout and its last waitUntil task, so a request keeps the app awake for the longer of the
    two."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        slug = self.slug

        from_ = self.from_.isoformat()

        until = self.until.isoformat()

        idle_timeout_seconds = self.idle_timeout_seconds

        window_seconds = self.window_seconds

        awake_seconds = self.awake_seconds

        awake_share = self.awake_share

        mb_seconds = self.mb_seconds

        gb_hours = self.gb_hours

        wakes = self.wakes

        unattributed_seconds = self.unattributed_seconds

        sources = []
        for sources_item_data in self.sources:
            sources_item = sources_item_data.to_dict()
            sources.append(sources_item)

        min_instances = self.min_instances

        other_routes_seconds = self.other_routes_seconds

        routes_limit = self.routes_limit

        routes = []
        for routes_item_data in self.routes:
            routes_item = routes_item_data.to_dict()
            routes.append(routes_item)

        window_clamped = self.window_clamped

        wait_until_timeout_seconds = self.wait_until_timeout_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "slug": slug,
                "from": from_,
                "until": until,
                "idle_timeout_seconds": idle_timeout_seconds,
                "window_seconds": window_seconds,
                "awake_seconds": awake_seconds,
                "awake_share": awake_share,
                "mb_seconds": mb_seconds,
                "gb_hours": gb_hours,
                "wakes": wakes,
                "unattributed_seconds": unattributed_seconds,
                "sources": sources,
                "min_instances": min_instances,
                "other_routes_seconds": other_routes_seconds,
                "routes_limit": routes_limit,
                "routes": routes,
            }
        )
        if window_clamped is not UNSET:
            field_dict["window_clamped"] = window_clamped
        if wait_until_timeout_seconds is not UNSET:
            field_dict["wait_until_timeout_seconds"] = wait_until_timeout_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_awake_route import RouteAwakeRoute
        from ..models.route_awake_source import RouteAwakeSource

        d = dict(src_dict)
        slug = d.pop("slug")

        from_ = datetime.datetime.fromisoformat(d.pop("from"))

        until = datetime.datetime.fromisoformat(d.pop("until"))

        idle_timeout_seconds = d.pop("idle_timeout_seconds")

        window_seconds = d.pop("window_seconds")

        awake_seconds = d.pop("awake_seconds")

        awake_share = d.pop("awake_share")

        mb_seconds = d.pop("mb_seconds")

        gb_hours = d.pop("gb_hours")

        wakes = d.pop("wakes")

        unattributed_seconds = d.pop("unattributed_seconds")

        sources = []
        _sources = d.pop("sources")
        for sources_item_data in _sources:
            sources_item = RouteAwakeSource.from_dict(sources_item_data)

            sources.append(sources_item)

        min_instances = d.pop("min_instances")

        other_routes_seconds = d.pop("other_routes_seconds")

        routes_limit = d.pop("routes_limit")

        routes = []
        _routes = d.pop("routes")
        for routes_item_data in _routes:
            routes_item = RouteAwakeRoute.from_dict(routes_item_data)

            routes.append(routes_item)

        window_clamped = d.pop("window_clamped", UNSET)

        wait_until_timeout_seconds = d.pop("wait_until_timeout_seconds", UNSET)

        route_awake_response = cls(
            slug=slug,
            from_=from_,
            until=until,
            idle_timeout_seconds=idle_timeout_seconds,
            window_seconds=window_seconds,
            awake_seconds=awake_seconds,
            awake_share=awake_share,
            mb_seconds=mb_seconds,
            gb_hours=gb_hours,
            wakes=wakes,
            unattributed_seconds=unattributed_seconds,
            sources=sources,
            min_instances=min_instances,
            other_routes_seconds=other_routes_seconds,
            routes_limit=routes_limit,
            routes=routes,
            window_clamped=window_clamped,
            wait_until_timeout_seconds=wait_until_timeout_seconds,
        )

        route_awake_response.additional_properties = d
        return route_awake_response

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
