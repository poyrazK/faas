from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.declared_route import DeclaredRoute


T = TypeVar("T", bound="EnvironmentRouteContract")


@_attrs_define
class EnvironmentRouteContract:
    """Atomic environment-scoped declared-route collection."""

    only_allow_declared_routes: bool
    declared_routes: list[DeclaredRoute]

    def to_dict(self) -> dict[str, Any]:
        only_allow_declared_routes = self.only_allow_declared_routes

        declared_routes = []
        for declared_routes_item_data in self.declared_routes:
            declared_routes_item = declared_routes_item_data.to_dict()
            declared_routes.append(declared_routes_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "only_allow_declared_routes": only_allow_declared_routes,
                "declared_routes": declared_routes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.declared_route import DeclaredRoute

        d = dict(src_dict)
        only_allow_declared_routes = d.pop("only_allow_declared_routes")

        declared_routes = []
        _declared_routes = d.pop("declared_routes")
        for declared_routes_item_data in _declared_routes:
            declared_routes_item = DeclaredRoute.from_dict(declared_routes_item_data)

            declared_routes.append(declared_routes_item)

        environment_route_contract = cls(
            only_allow_declared_routes=only_allow_declared_routes,
            declared_routes=declared_routes,
        )

        return environment_route_contract
