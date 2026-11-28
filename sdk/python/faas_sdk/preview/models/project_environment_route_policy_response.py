from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_environment_route_policy_response_ownership import (
    ProjectEnvironmentRoutePolicyResponseOwnership,
    check_project_environment_route_policy_response_ownership,
)

if TYPE_CHECKING:
    from ..models.declared_route import DeclaredRoute


T = TypeVar("T", bound="ProjectEnvironmentRoutePolicyResponse")


@_attrs_define
class ProjectEnvironmentRoutePolicyResponse:
    """Effective declared-route contract and whether it is environment-owned."""

    ownership: ProjectEnvironmentRoutePolicyResponseOwnership
    only_allow_declared_routes: bool
    declared_routes: list[DeclaredRoute]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        ownership: str = self.ownership

        only_allow_declared_routes = self.only_allow_declared_routes

        declared_routes = []
        for declared_routes_item_data in self.declared_routes:
            declared_routes_item = declared_routes_item_data.to_dict()
            declared_routes.append(declared_routes_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "ownership": ownership,
                "only_allow_declared_routes": only_allow_declared_routes,
                "declared_routes": declared_routes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.declared_route import DeclaredRoute

        d = dict(src_dict)
        ownership = check_project_environment_route_policy_response_ownership(d.pop("ownership"))

        only_allow_declared_routes = d.pop("only_allow_declared_routes")

        declared_routes = []
        _declared_routes = d.pop("declared_routes")
        for declared_routes_item_data in _declared_routes:
            declared_routes_item = DeclaredRoute.from_dict(declared_routes_item_data)

            declared_routes.append(declared_routes_item)

        project_environment_route_policy_response = cls(
            ownership=ownership,
            only_allow_declared_routes=only_allow_declared_routes,
            declared_routes=declared_routes,
        )

        project_environment_route_policy_response.additional_properties = d
        return project_environment_route_policy_response

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
