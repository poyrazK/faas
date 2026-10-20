from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.debug_compare_route_stats import DebugCompareRouteStats


T = TypeVar("T", bound="DebugCompareResponse")


@_attrs_define
class DebugCompareResponse:
    """POST response from /v1/apps/{slug}/debug/compare (ADR-127 / PR-B)."""

    source: UUID
    mirror: UUID
    routes: list[DebugCompareRouteStats]
    app_id: UUID | Unset = UNSET
    """App identity; present on servers supporting comparison-window metadata (ADR-1623)."""
    since: str | Unset = UNSET
    """Effective lookback after the current retention cutoff; native default is 1h."""
    window_start: datetime.datetime | Unset = UNSET
    window_end: datetime.datetime | Unset = UNSET
    retention_clamped: bool | Unset = UNSET
    """Whether the requested start was moved to the current plan-retention cutoff."""
    route_filter: str | Unset = UNSET
    """Applied exact route filter; omitted for all routes."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source = str(self.source)

        mirror = str(self.mirror)

        routes = []
        for routes_item_data in self.routes:
            routes_item = routes_item_data.to_dict()
            routes.append(routes_item)

        app_id: str | Unset = UNSET
        if not isinstance(self.app_id, Unset):
            app_id = str(self.app_id)

        since = self.since

        window_start: str | Unset = UNSET
        if not isinstance(self.window_start, Unset):
            window_start = self.window_start.isoformat()

        window_end: str | Unset = UNSET
        if not isinstance(self.window_end, Unset):
            window_end = self.window_end.isoformat()

        retention_clamped = self.retention_clamped

        route_filter = self.route_filter

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "source": source,
                "mirror": mirror,
                "routes": routes,
            }
        )
        if app_id is not UNSET:
            field_dict["app_id"] = app_id
        if since is not UNSET:
            field_dict["since"] = since
        if window_start is not UNSET:
            field_dict["window_start"] = window_start
        if window_end is not UNSET:
            field_dict["window_end"] = window_end
        if retention_clamped is not UNSET:
            field_dict["retention_clamped"] = retention_clamped
        if route_filter is not UNSET:
            field_dict["route_filter"] = route_filter

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.debug_compare_route_stats import DebugCompareRouteStats

        d = dict(src_dict)
        source = UUID(d.pop("source"))

        mirror = UUID(d.pop("mirror"))

        routes = []
        _routes = d.pop("routes")
        for routes_item_data in _routes:
            routes_item = DebugCompareRouteStats.from_dict(routes_item_data)

            routes.append(routes_item)

        _app_id = d.pop("app_id", UNSET)
        app_id: UUID | Unset
        if isinstance(_app_id, Unset):
            app_id = UNSET
        else:
            app_id = UUID(_app_id)

        since = d.pop("since", UNSET)

        _window_start = d.pop("window_start", UNSET)
        window_start: datetime.datetime | Unset
        if isinstance(_window_start, Unset):
            window_start = UNSET
        else:
            window_start = datetime.datetime.fromisoformat(_window_start)

        _window_end = d.pop("window_end", UNSET)
        window_end: datetime.datetime | Unset
        if isinstance(_window_end, Unset):
            window_end = UNSET
        else:
            window_end = datetime.datetime.fromisoformat(_window_end)

        retention_clamped = d.pop("retention_clamped", UNSET)

        route_filter = d.pop("route_filter", UNSET)

        debug_compare_response = cls(
            source=source,
            mirror=mirror,
            routes=routes,
            app_id=app_id,
            since=since,
            window_start=window_start,
            window_end=window_end,
            retention_clamped=retention_clamped,
            route_filter=route_filter,
        )

        debug_compare_response.additional_properties = d
        return debug_compare_response

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
