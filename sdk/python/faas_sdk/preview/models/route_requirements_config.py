from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.route_requirements_config_version import (
    RouteRequirementsConfigVersion,
    check_route_requirements_config_version,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_group import RouteGroup
    from ..models.route_public_exception import RoutePublicException
    from ..models.route_requirement import RouteRequirement


T = TypeVar("T", bound="RouteRequirementsConfig")


@_attrs_define
class RouteRequirementsConfig:
    """Version 1 requires 1..500 concrete routes. Version 2 assigns every captured operation to groups, concrete routes, or
    public exceptions; overlapping groups are conjunctive.

    """

    version: RouteRequirementsConfigVersion
    routes: list[RouteRequirement] | Unset = UNSET
    groups: list[RouteGroup] | Unset = UNSET
    public: list[RoutePublicException] | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        routes: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.routes, Unset):
            routes = []
            for routes_item_data in self.routes:
                routes_item = routes_item_data.to_dict()
                routes.append(routes_item)

        groups: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.groups, Unset):
            groups = []
            for groups_item_data in self.groups:
                groups_item = groups_item_data.to_dict()
                groups.append(groups_item)

        public: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.public, Unset):
            public = []
            for public_item_data in self.public:
                public_item = public_item_data.to_dict()
                public.append(public_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "version": version,
            }
        )
        if routes is not UNSET:
            field_dict["routes"] = routes
        if groups is not UNSET:
            field_dict["groups"] = groups
        if public is not UNSET:
            field_dict["public"] = public

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_group import RouteGroup
        from ..models.route_public_exception import RoutePublicException
        from ..models.route_requirement import RouteRequirement

        d = dict(src_dict)
        version = check_route_requirements_config_version(d.pop("version"))

        _routes = d.pop("routes", UNSET)
        routes: list[RouteRequirement] | Unset = UNSET
        if _routes is not UNSET:
            routes = []
            for routes_item_data in _routes:
                routes_item = RouteRequirement.from_dict(routes_item_data)

                routes.append(routes_item)

        _groups = d.pop("groups", UNSET)
        groups: list[RouteGroup] | Unset = UNSET
        if _groups is not UNSET:
            groups = []
            for groups_item_data in _groups:
                groups_item = RouteGroup.from_dict(groups_item_data)

                groups.append(groups_item)

        _public = d.pop("public", UNSET)
        public: list[RoutePublicException] | Unset = UNSET
        if _public is not UNSET:
            public = []
            for public_item_data in _public:
                public_item = RoutePublicException.from_dict(public_item_data)

                public.append(public_item)

        route_requirements_config = cls(
            version=version,
            routes=routes,
            groups=groups,
            public=public,
        )

        return route_requirements_config
