from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_resource_split_kind import RouteResourceSplitKind, check_route_resource_split_kind
from ..types import UNSET, Unset

T = TypeVar("T", bound="RouteResourceSplit")


@_attrs_define
class RouteResourceSplit:
    """Present when one route holds at least 80% of at least 3 memory peaks while running in at most 30% of minutes, and
    the rest of the app would fit in less memory: move that route to its own app and size the app down (kind memory).
    When no memory split qualifies, the same rule over CPU peaks suggests moving the route for isolation; both apps keep
    the current size (kind cpu).

    """

    kind: RouteResourceSplitKind
    """memory sizes both apps for what they need; cpu isolates a route whose CPU bursts slow the app's other
    routes."""
    route: str
    path_glob: str
    """Edge-rule path match for the route; path parameters become one-segment wildcards."""
    route_ram_mb: int
    """Suggested size of the new app: the route's p95 peak plus 25%, rounded up to 64 MB."""
    current_ram_mb: int
    main_ram_mb: int
    """Suggested size of the app without the route: its median peak plus the p95 rise without the route, plus 25%,
    rounded up to 64 MB."""
    high_memory_share: float
    presence_share: float
    high_memory_minutes: int
    reason: str
    method: str | Unset = UNSET
    route_ram_over_plan: bool | Unset = UNSET
    """The route needs more memory than the plan's largest app."""
    high_cpu_share: float | Unset = UNSET
    """Share of attributed CPU peaks the route was in flight for; set on cpu splits."""
    high_cpu_minutes: int | Unset = UNSET
    """CPU peaks the route was in flight for; set on cpu splits."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        route = self.route

        path_glob = self.path_glob

        route_ram_mb = self.route_ram_mb

        current_ram_mb = self.current_ram_mb

        main_ram_mb = self.main_ram_mb

        high_memory_share = self.high_memory_share

        presence_share = self.presence_share

        high_memory_minutes = self.high_memory_minutes

        reason = self.reason

        method = self.method

        route_ram_over_plan = self.route_ram_over_plan

        high_cpu_share = self.high_cpu_share

        high_cpu_minutes = self.high_cpu_minutes

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "kind": kind,
                "route": route,
                "path_glob": path_glob,
                "route_ram_mb": route_ram_mb,
                "current_ram_mb": current_ram_mb,
                "main_ram_mb": main_ram_mb,
                "high_memory_share": high_memory_share,
                "presence_share": presence_share,
                "high_memory_minutes": high_memory_minutes,
                "reason": reason,
            }
        )
        if method is not UNSET:
            field_dict["method"] = method
        if route_ram_over_plan is not UNSET:
            field_dict["route_ram_over_plan"] = route_ram_over_plan
        if high_cpu_share is not UNSET:
            field_dict["high_cpu_share"] = high_cpu_share
        if high_cpu_minutes is not UNSET:
            field_dict["high_cpu_minutes"] = high_cpu_minutes

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        kind = check_route_resource_split_kind(d.pop("kind"))

        route = d.pop("route")

        path_glob = d.pop("path_glob")

        route_ram_mb = d.pop("route_ram_mb")

        current_ram_mb = d.pop("current_ram_mb")

        main_ram_mb = d.pop("main_ram_mb")

        high_memory_share = d.pop("high_memory_share")

        presence_share = d.pop("presence_share")

        high_memory_minutes = d.pop("high_memory_minutes")

        reason = d.pop("reason")

        method = d.pop("method", UNSET)

        route_ram_over_plan = d.pop("route_ram_over_plan", UNSET)

        high_cpu_share = d.pop("high_cpu_share", UNSET)

        high_cpu_minutes = d.pop("high_cpu_minutes", UNSET)

        route_resource_split = cls(
            kind=kind,
            route=route,
            path_glob=path_glob,
            route_ram_mb=route_ram_mb,
            current_ram_mb=current_ram_mb,
            main_ram_mb=main_ram_mb,
            high_memory_share=high_memory_share,
            presence_share=presence_share,
            high_memory_minutes=high_memory_minutes,
            reason=reason,
            method=method,
            route_ram_over_plan=route_ram_over_plan,
            high_cpu_share=high_cpu_share,
            high_cpu_minutes=high_cpu_minutes,
        )

        route_resource_split.additional_properties = d
        return route_resource_split

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
