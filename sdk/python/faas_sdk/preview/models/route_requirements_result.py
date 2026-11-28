from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_requirements_finding import RouteRequirementsFinding


T = TypeVar("T", bound="RouteRequirementsResult")


@_attrs_define
class RouteRequirementsResult:
    """Per-request requirement evaluation including satisfied, violated, or unknown findings."""

    method: str
    path: str
    status: str
    checks: list[RouteRequirementsFinding]
    name: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        method = self.method

        path = self.path

        status = self.status

        checks = []
        for checks_item_data in self.checks:
            checks_item = checks_item_data.to_dict()
            checks.append(checks_item)

        name = self.name

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "method": method,
                "path": path,
                "status": status,
                "checks": checks,
            }
        )
        if name is not UNSET:
            field_dict["name"] = name

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_requirements_finding import RouteRequirementsFinding

        d = dict(src_dict)
        method = d.pop("method")

        path = d.pop("path")

        status = d.pop("status")

        checks = []
        _checks = d.pop("checks")
        for checks_item_data in _checks:
            checks_item = RouteRequirementsFinding.from_dict(checks_item_data)

            checks.append(checks_item)

        name = d.pop("name", UNSET)

        route_requirements_result = cls(
            method=method,
            path=path,
            status=status,
            checks=checks,
            name=name,
        )

        route_requirements_result.additional_properties = d
        return route_requirements_result

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
