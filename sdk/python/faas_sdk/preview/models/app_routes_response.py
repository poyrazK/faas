from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_routes_response_source import AppRoutesResponseSource, check_app_routes_response_source
from ..types import UNSET, Unset

T = TypeVar("T", bound="AppRoutesResponse")


@_attrs_define
class AppRoutesResponse:
    """Fleet-wide per-route label snapshot (ADR-093). The control-plane
    Prometheus instance aggregates every active, scrape-ready compute
    gateway; collector counts distinguish complete, partial, and
    unavailable observations from a healthy no-traffic result.
    Each item is `"<METHOD> <PATH>"` (pre-edge-rule-rewrite) for
    an admitted route, or the reserved `"__route_other__"` overflow
    bucket label. The fleet union is bounded again at 50 distinct real
    routes plus the reserved overflow bucket (ADR-093 D2).

    `cap_hit` is true when a compute collector emitted the overflow
    bucket or the fleet union reached the 50-route bound. It is false
    on `source: unavailable`, where cap state is unknown.

    """

    slug: str
    routes: list[str]
    source: AppRoutesResponseSource
    collectors_expected: int
    """Active scrape-ready compute route collectors in the registry."""
    collectors_healthy: int
    """Expected collectors with a current successful Prometheus scrape."""
    cap_hit: bool = False
    """True when the fleet route union reaches `RouteMetricsPerAppCap`
    (50) or a collector reports `__route_other__`. False on
    `source: unavailable`, where cap state is unknown.
    """
    scope: str | Unset = UNSET
    project_id: str | Unset = UNSET
    environment_id: str | Unset = UNSET
    coverage: str | Unset = UNSET
    """Explicit scopes use deployment_attributed_history over the supplied observation window."""
    window_start: datetime.datetime | Unset = UNSET
    window_end: datetime.datetime | Unset = UNSET
    app_id: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        slug = self.slug

        routes = self.routes

        source: str = self.source

        collectors_expected = self.collectors_expected

        collectors_healthy = self.collectors_healthy

        cap_hit = self.cap_hit

        scope = self.scope

        project_id = self.project_id

        environment_id = self.environment_id

        coverage = self.coverage

        window_start: str | Unset = UNSET
        if not isinstance(self.window_start, Unset):
            window_start = self.window_start.isoformat()

        window_end: str | Unset = UNSET
        if not isinstance(self.window_end, Unset):
            window_end = self.window_end.isoformat()

        app_id = self.app_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "slug": slug,
                "routes": routes,
                "source": source,
                "collectors_expected": collectors_expected,
                "collectors_healthy": collectors_healthy,
                "cap_hit": cap_hit,
            }
        )
        if scope is not UNSET:
            field_dict["scope"] = scope
        if project_id is not UNSET:
            field_dict["project_id"] = project_id
        if environment_id is not UNSET:
            field_dict["environment_id"] = environment_id
        if coverage is not UNSET:
            field_dict["coverage"] = coverage
        if window_start is not UNSET:
            field_dict["window_start"] = window_start
        if window_end is not UNSET:
            field_dict["window_end"] = window_end
        if app_id is not UNSET:
            field_dict["app_id"] = app_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        slug = d.pop("slug")

        routes = cast(list[str], d.pop("routes"))

        source = check_app_routes_response_source(d.pop("source"))

        collectors_expected = d.pop("collectors_expected")

        collectors_healthy = d.pop("collectors_healthy")

        cap_hit = d.pop("cap_hit")

        scope = d.pop("scope", UNSET)

        project_id = d.pop("project_id", UNSET)

        environment_id = d.pop("environment_id", UNSET)

        coverage = d.pop("coverage", UNSET)

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

        app_id = d.pop("app_id", UNSET)

        app_routes_response = cls(
            slug=slug,
            routes=routes,
            source=source,
            collectors_expected=collectors_expected,
            collectors_healthy=collectors_healthy,
            cap_hit=cap_hit,
            scope=scope,
            project_id=project_id,
            environment_id=environment_id,
            coverage=coverage,
            window_start=window_start,
            window_end=window_end,
            app_id=app_id,
        )

        app_routes_response.additional_properties = d
        return app_routes_response

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
