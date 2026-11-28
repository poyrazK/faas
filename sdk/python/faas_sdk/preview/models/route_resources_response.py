from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_resource_attribution import RouteResourceAttribution
    from ..models.route_resource_split import RouteResourceSplit


T = TypeVar("T", bound="RouteResourcesResponse")


@_attrs_define
class RouteResourcesResponse:
    """Route attribution of an app's memory and CPU peaks over a window."""

    slug: str
    from_: datetime.datetime
    """Inclusive start of the analysed window."""
    until: datetime.datetime
    """Exclusive end of the analysed window."""
    truncated: bool
    """The window held more minutes than one read analyses; the newest were used."""
    resource_minutes: int
    """Instance-minutes with memory and CPU samples."""
    active_minutes: int
    """Instance-minutes with samples that also served requests."""
    route_minutes_without_resources: int
    """Instance-minutes that served requests but have no samples; left out of every share."""
    mem_peak_p50_mb: int
    mem_peak_p95_mb: int
    mem_peak_max_mb: int
    mem_limit_mb: int
    """The app's memory limit; 0 when unknown."""
    high_memory_rise_mb: int
    """Rise above the minute's starting memory that counted as a memory peak in this window."""
    high_memory_minutes: int
    unattributed_high_memory_minutes: int
    """Memory peaks with no request in flight, such as background work."""
    cpu_peak_p95_millicores: int
    cpu_limit_millicores: int
    """The app's CPU limit; 0 when unknown."""
    high_cpu_minutes: int
    routes: list[RouteResourceAttribution]
    routes_omitted: int
    """Routes past the 50 reported."""
    split: RouteResourceSplit | Unset = UNSET
    """Present when one route holds at least 80% of at least 3 memory peaks while running in at most 30% of
    minutes, and the rest of the app would fit in less memory: move that route to its own app and size the app down
    (kind memory). When no memory split qualifies, the same rule over CPU peaks suggests moving the route for
    isolation; both apps keep the current size (kind cpu)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        slug = self.slug

        from_ = self.from_.isoformat()

        until = self.until.isoformat()

        truncated = self.truncated

        resource_minutes = self.resource_minutes

        active_minutes = self.active_minutes

        route_minutes_without_resources = self.route_minutes_without_resources

        mem_peak_p50_mb = self.mem_peak_p50_mb

        mem_peak_p95_mb = self.mem_peak_p95_mb

        mem_peak_max_mb = self.mem_peak_max_mb

        mem_limit_mb = self.mem_limit_mb

        high_memory_rise_mb = self.high_memory_rise_mb

        high_memory_minutes = self.high_memory_minutes

        unattributed_high_memory_minutes = self.unattributed_high_memory_minutes

        cpu_peak_p95_millicores = self.cpu_peak_p95_millicores

        cpu_limit_millicores = self.cpu_limit_millicores

        high_cpu_minutes = self.high_cpu_minutes

        routes = []
        for routes_item_data in self.routes:
            routes_item = routes_item_data.to_dict()
            routes.append(routes_item)

        routes_omitted = self.routes_omitted

        split: dict[str, Any] | Unset = UNSET
        if not isinstance(self.split, Unset):
            split = self.split.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "slug": slug,
                "from": from_,
                "until": until,
                "truncated": truncated,
                "resource_minutes": resource_minutes,
                "active_minutes": active_minutes,
                "route_minutes_without_resources": route_minutes_without_resources,
                "mem_peak_p50_mb": mem_peak_p50_mb,
                "mem_peak_p95_mb": mem_peak_p95_mb,
                "mem_peak_max_mb": mem_peak_max_mb,
                "mem_limit_mb": mem_limit_mb,
                "high_memory_rise_mb": high_memory_rise_mb,
                "high_memory_minutes": high_memory_minutes,
                "unattributed_high_memory_minutes": unattributed_high_memory_minutes,
                "cpu_peak_p95_millicores": cpu_peak_p95_millicores,
                "cpu_limit_millicores": cpu_limit_millicores,
                "high_cpu_minutes": high_cpu_minutes,
                "routes": routes,
                "routes_omitted": routes_omitted,
            }
        )
        if split is not UNSET:
            field_dict["split"] = split

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_resource_attribution import RouteResourceAttribution
        from ..models.route_resource_split import RouteResourceSplit

        d = dict(src_dict)
        slug = d.pop("slug")

        from_ = datetime.datetime.fromisoformat(d.pop("from"))

        until = datetime.datetime.fromisoformat(d.pop("until"))

        truncated = d.pop("truncated")

        resource_minutes = d.pop("resource_minutes")

        active_minutes = d.pop("active_minutes")

        route_minutes_without_resources = d.pop("route_minutes_without_resources")

        mem_peak_p50_mb = d.pop("mem_peak_p50_mb")

        mem_peak_p95_mb = d.pop("mem_peak_p95_mb")

        mem_peak_max_mb = d.pop("mem_peak_max_mb")

        mem_limit_mb = d.pop("mem_limit_mb")

        high_memory_rise_mb = d.pop("high_memory_rise_mb")

        high_memory_minutes = d.pop("high_memory_minutes")

        unattributed_high_memory_minutes = d.pop("unattributed_high_memory_minutes")

        cpu_peak_p95_millicores = d.pop("cpu_peak_p95_millicores")

        cpu_limit_millicores = d.pop("cpu_limit_millicores")

        high_cpu_minutes = d.pop("high_cpu_minutes")

        routes = []
        _routes = d.pop("routes")
        for routes_item_data in _routes:
            routes_item = RouteResourceAttribution.from_dict(routes_item_data)

            routes.append(routes_item)

        routes_omitted = d.pop("routes_omitted")

        _split = d.pop("split", UNSET)
        split: RouteResourceSplit | Unset
        if isinstance(_split, Unset):
            split = UNSET
        else:
            split = RouteResourceSplit.from_dict(_split)

        route_resources_response = cls(
            slug=slug,
            from_=from_,
            until=until,
            truncated=truncated,
            resource_minutes=resource_minutes,
            active_minutes=active_minutes,
            route_minutes_without_resources=route_minutes_without_resources,
            mem_peak_p50_mb=mem_peak_p50_mb,
            mem_peak_p95_mb=mem_peak_p95_mb,
            mem_peak_max_mb=mem_peak_max_mb,
            mem_limit_mb=mem_limit_mb,
            high_memory_rise_mb=high_memory_rise_mb,
            high_memory_minutes=high_memory_minutes,
            unattributed_high_memory_minutes=unattributed_high_memory_minutes,
            cpu_peak_p95_millicores=cpu_peak_p95_millicores,
            cpu_limit_millicores=cpu_limit_millicores,
            high_cpu_minutes=high_cpu_minutes,
            routes=routes,
            routes_omitted=routes_omitted,
            split=split,
        )

        route_resources_response.additional_properties = d
        return route_resources_response

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
