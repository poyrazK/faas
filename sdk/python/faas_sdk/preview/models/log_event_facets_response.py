from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.log_facet_count import LogFacetCount


T = TypeVar("T", bound="LogEventFacetsResponse")


@_attrs_define
class LogEventFacetsResponse:
    """Log event counts by facet over one window."""

    since: datetime.datetime
    """Facet window start after clamping to retention."""
    until: datetime.datetime
    total: int
    sources: list[LogFacetCount]
    levels: list[LogFacetCount]
    status_classes: list[LogFacetCount]
    routes: list[LogFacetCount]
    routes_truncated: bool
    """More routes matched than the ten returned."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        since = self.since.isoformat()

        until = self.until.isoformat()

        total = self.total

        sources = []
        for sources_item_data in self.sources:
            sources_item = sources_item_data.to_dict()
            sources.append(sources_item)

        levels = []
        for levels_item_data in self.levels:
            levels_item = levels_item_data.to_dict()
            levels.append(levels_item)

        status_classes = []
        for status_classes_item_data in self.status_classes:
            status_classes_item = status_classes_item_data.to_dict()
            status_classes.append(status_classes_item)

        routes = []
        for routes_item_data in self.routes:
            routes_item = routes_item_data.to_dict()
            routes.append(routes_item)

        routes_truncated = self.routes_truncated

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "since": since,
                "until": until,
                "total": total,
                "sources": sources,
                "levels": levels,
                "status_classes": status_classes,
                "routes": routes,
                "routes_truncated": routes_truncated,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.log_facet_count import LogFacetCount

        d = dict(src_dict)
        since = datetime.datetime.fromisoformat(d.pop("since"))

        until = datetime.datetime.fromisoformat(d.pop("until"))

        total = d.pop("total")

        sources = []
        _sources = d.pop("sources")
        for sources_item_data in _sources:
            sources_item = LogFacetCount.from_dict(sources_item_data)

            sources.append(sources_item)

        levels = []
        _levels = d.pop("levels")
        for levels_item_data in _levels:
            levels_item = LogFacetCount.from_dict(levels_item_data)

            levels.append(levels_item)

        status_classes = []
        _status_classes = d.pop("status_classes")
        for status_classes_item_data in _status_classes:
            status_classes_item = LogFacetCount.from_dict(status_classes_item_data)

            status_classes.append(status_classes_item)

        routes = []
        _routes = d.pop("routes")
        for routes_item_data in _routes:
            routes_item = LogFacetCount.from_dict(routes_item_data)

            routes.append(routes_item)

        routes_truncated = d.pop("routes_truncated")

        log_event_facets_response = cls(
            since=since,
            until=until,
            total=total,
            sources=sources,
            levels=levels,
            status_classes=status_classes,
            routes=routes,
            routes_truncated=routes_truncated,
        )

        log_event_facets_response.additional_properties = d
        return log_event_facets_response

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
