from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.route_group_methods_item import RouteGroupMethodsItem, check_route_group_methods_item

if TYPE_CHECKING:
    from ..models.route_checks import RouteChecks


T = TypeVar("T", bound="RouteGroup")


@_attrs_define
class RouteGroup:
    """Named policy requirements for explicit methods below a canonical literal prefix ending in slash."""

    name: str
    path_prefix: str
    methods: list[RouteGroupMethodsItem]
    require: RouteChecks
    """At least one policy requirement; application authorization remains outside configuration verification."""

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        path_prefix = self.path_prefix

        methods = []
        for methods_item_data in self.methods:
            methods_item: str = methods_item_data
            methods.append(methods_item)

        require = self.require.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "path_prefix": path_prefix,
                "methods": methods,
                "require": require,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_checks import RouteChecks

        d = dict(src_dict)
        name = d.pop("name")

        path_prefix = d.pop("path_prefix")

        methods = []
        _methods = d.pop("methods")
        for methods_item_data in _methods:
            methods_item = check_route_group_methods_item(methods_item_data)

            methods.append(methods_item)

        require = RouteChecks.from_dict(d.pop("require"))

        route_group = cls(
            name=name,
            path_prefix=path_prefix,
            methods=methods,
            require=require,
        )

        return route_group
