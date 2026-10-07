from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.route_requirement_method import RouteRequirementMethod, check_route_requirement_method
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_checks import RouteChecks


T = TypeVar("T", bound="RouteRequirement")


@_attrs_define
class RouteRequirement:
    """Named request shape and required configured authentication, throttle, or execution budget."""

    method: RouteRequirementMethod
    path: str
    """Concrete decoded platform-host path without templates or globs."""
    require: RouteChecks
    """At least one policy requirement; application authorization remains outside configuration verification."""
    name: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        method: str = self.method

        path = self.path

        require = self.require.to_dict()

        name = self.name

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "method": method,
                "path": path,
                "require": require,
            }
        )
        if name is not UNSET:
            field_dict["name"] = name

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_checks import RouteChecks

        d = dict(src_dict)
        method = check_route_requirement_method(d.pop("method"))

        path = d.pop("path")

        require = RouteChecks.from_dict(d.pop("require"))

        name = d.pop("name", UNSET)

        route_requirement = cls(
            method=method,
            path=path,
            require=require,
            name=name,
        )

        return route_requirement
