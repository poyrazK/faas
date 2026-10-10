from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_priorities_response_source import (
    RoutePrioritiesResponseSource,
    check_route_priorities_response_source,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_priority_rule import RoutePriorityRule


T = TypeVar("T", bound="RoutePrioritiesResponse")


@_attrs_define
class RoutePrioritiesResponse:
    """An app's effective route priorities and where they come from."""

    slug: str
    source: RoutePrioritiesResponseSource
    routes: list[RoutePriorityRule]
    updated_at: datetime.datetime | Unset = UNSET
    """When saved rules last changed; omitted for the route-health default."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        slug = self.slug

        source: str = self.source

        routes = []
        for routes_item_data in self.routes:
            routes_item = routes_item_data.to_dict()
            routes.append(routes_item)

        updated_at: str | Unset = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "slug": slug,
                "source": source,
                "routes": routes,
            }
        )
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_priority_rule import RoutePriorityRule

        d = dict(src_dict)
        slug = d.pop("slug")

        source = check_route_priorities_response_source(d.pop("source"))

        routes = []
        _routes = d.pop("routes")
        for routes_item_data in _routes:
            routes_item = RoutePriorityRule.from_dict(routes_item_data)

            routes.append(routes_item)

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: datetime.datetime | Unset
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = datetime.datetime.fromisoformat(_updated_at)

        route_priorities_response = cls(
            slug=slug,
            source=source,
            routes=routes,
            updated_at=updated_at,
        )

        route_priorities_response.additional_properties = d
        return route_priorities_response

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
