from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_assigned_check import RouteAssignedCheck


T = TypeVar("T", bound="RouteAssignment")


@_attrs_define
class RouteAssignment:
    """Captured operation assignment and group provenance for checks in the same report row."""

    method: str
    path: str
    scope: str
    groups: list[str] | Unset = UNSET
    checks: list[RouteAssignedCheck] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        method = self.method

        path = self.path

        scope = self.scope

        groups: list[str] | Unset = UNSET
        if not isinstance(self.groups, Unset):
            groups = self.groups

        checks: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.checks, Unset):
            checks = []
            for checks_item_data in self.checks:
                checks_item = checks_item_data.to_dict()
                checks.append(checks_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "method": method,
                "path": path,
                "scope": scope,
            }
        )
        if groups is not UNSET:
            field_dict["groups"] = groups
        if checks is not UNSET:
            field_dict["checks"] = checks

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_assigned_check import RouteAssignedCheck

        d = dict(src_dict)
        method = d.pop("method")

        path = d.pop("path")

        scope = d.pop("scope")

        groups = cast(list[str], d.pop("groups", UNSET))

        _checks = d.pop("checks", UNSET)
        checks: list[RouteAssignedCheck] | Unset = UNSET
        if _checks is not UNSET:
            checks = []
            for checks_item_data in _checks:
                checks_item = RouteAssignedCheck.from_dict(checks_item_data)

                checks.append(checks_item)

        route_assignment = cls(
            method=method,
            path=path,
            scope=scope,
            groups=groups,
            checks=checks,
        )

        route_assignment.additional_properties = d
        return route_assignment

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
