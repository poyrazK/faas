from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_assignment import RouteAssignment
    from ..models.route_coverage_inventory import RouteCoverageInventory
    from ..models.route_group_result import RouteGroupResult
    from ..models.route_requirements_result import RouteRequirementsResult


T = TypeVar("T", bound="RouteRequirementsReport")


@_attrs_define
class RouteRequirementsReport:
    """Configuration evidence for concrete requests or every captured operation with group assignments and bounded policy
    scope.

    """

    version: int
    sha256: str
    policy_scope: str
    status: str
    scope: str
    routes: list[RouteRequirementsResult]
    host: str | Unset = UNSET
    coverage: RouteCoverageInventory | Unset = UNSET
    """Provenance and completeness of the selected captured contract; raw contract content is excluded."""
    groups: list[RouteGroupResult] | Unset = UNSET
    assignments: list[RouteAssignment] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version = self.version

        sha256 = self.sha256

        policy_scope = self.policy_scope

        status = self.status

        scope = self.scope

        routes = []
        for routes_item_data in self.routes:
            routes_item = routes_item_data.to_dict()
            routes.append(routes_item)

        host = self.host

        coverage: dict[str, Any] | Unset = UNSET
        if not isinstance(self.coverage, Unset):
            coverage = self.coverage.to_dict()

        groups: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.groups, Unset):
            groups = []
            for groups_item_data in self.groups:
                groups_item = groups_item_data.to_dict()
                groups.append(groups_item)

        assignments: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.assignments, Unset):
            assignments = []
            for assignments_item_data in self.assignments:
                assignments_item = assignments_item_data.to_dict()
                assignments.append(assignments_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "sha256": sha256,
                "policy_scope": policy_scope,
                "status": status,
                "scope": scope,
                "routes": routes,
            }
        )
        if host is not UNSET:
            field_dict["host"] = host
        if coverage is not UNSET:
            field_dict["coverage"] = coverage
        if groups is not UNSET:
            field_dict["groups"] = groups
        if assignments is not UNSET:
            field_dict["assignments"] = assignments

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_assignment import RouteAssignment
        from ..models.route_coverage_inventory import RouteCoverageInventory
        from ..models.route_group_result import RouteGroupResult
        from ..models.route_requirements_result import RouteRequirementsResult

        d = dict(src_dict)
        version = d.pop("version")

        sha256 = d.pop("sha256")

        policy_scope = d.pop("policy_scope")

        status = d.pop("status")

        scope = d.pop("scope")

        routes = []
        _routes = d.pop("routes")
        for routes_item_data in _routes:
            routes_item = RouteRequirementsResult.from_dict(routes_item_data)

            routes.append(routes_item)

        host = d.pop("host", UNSET)

        _coverage = d.pop("coverage", UNSET)
        coverage: RouteCoverageInventory | Unset
        if isinstance(_coverage, Unset):
            coverage = UNSET
        else:
            coverage = RouteCoverageInventory.from_dict(_coverage)

        _groups = d.pop("groups", UNSET)
        groups: list[RouteGroupResult] | Unset = UNSET
        if _groups is not UNSET:
            groups = []
            for groups_item_data in _groups:
                groups_item = RouteGroupResult.from_dict(groups_item_data)

                groups.append(groups_item)

        _assignments = d.pop("assignments", UNSET)
        assignments: list[RouteAssignment] | Unset = UNSET
        if _assignments is not UNSET:
            assignments = []
            for assignments_item_data in _assignments:
                assignments_item = RouteAssignment.from_dict(assignments_item_data)

                assignments.append(assignments_item)

        route_requirements_report = cls(
            version=version,
            sha256=sha256,
            policy_scope=policy_scope,
            status=status,
            scope=scope,
            routes=routes,
            host=host,
            coverage=coverage,
            groups=groups,
            assignments=assignments,
        )

        route_requirements_report.additional_properties = d
        return route_requirements_report

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
