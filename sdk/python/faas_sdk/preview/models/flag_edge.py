from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.flag_edge_route import FlagEdgeRoute


T = TypeVar("T", bound="FlagEdge")


@_attrs_define
class FlagEdge:
    """Edge routing of a variant flag (ADR-20261124212701): the gateway evaluates the flag for GET and HEAD requests to a
    routed path and rewrites the path for the decided variant, so apps without an SDK serve it. Verified customers
    bucket as in an SDK; anonymous visitors by a first-party cookie. Needs the edge_flags platform flag to add or
    change; runtime bundles leave it out.

    """

    routes: list[FlagEdgeRoute]

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
        from ..models.flag_edge_route import FlagEdgeRoute

        d = dict(src_dict)
        routes = []
        _routes = d.pop("routes")
        for routes_item_data in _routes:
            routes_item = FlagEdgeRoute.from_dict(routes_item_data)

            routes.append(routes_item)

        flag_edge = cls(
            routes=routes,
        )

        return flag_edge
