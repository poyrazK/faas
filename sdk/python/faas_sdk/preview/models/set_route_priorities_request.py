from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.route_priority_rule import RoutePriorityRule


T = TypeVar("T", bound="SetRoutePrioritiesRequest")


@_attrs_define
class SetRoutePrioritiesRequest:
    """Replaces the saved route priorities; an empty list saves none."""

    routes: list[RoutePriorityRule]

    def to_dict(self) -> dict[str, Any]:
        routes = []
        for routes_item_data in self.routes:
            routes_item = routes_item_data.to_dict()
            routes.append(routes_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "routes": routes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_priority_rule import RoutePriorityRule

        d = dict(src_dict)
        routes = []
        _routes = d.pop("routes")
        for routes_item_data in _routes:
            routes_item = RoutePriorityRule.from_dict(routes_item_data)

            routes.append(routes_item)

        set_route_priorities_request = cls(
            routes=routes,
        )

        return set_route_priorities_request
