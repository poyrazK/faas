from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RouteGroupResult")


@_attrs_define
class RouteGroupResult:
    """Group coverage status and captured operation counts."""

    name: str
    status: str
    matched_routes: int
    exempt_routes: int
    code: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        status = self.status

        matched_routes = self.matched_routes

        exempt_routes = self.exempt_routes

        code = self.code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "status": status,
                "matched_routes": matched_routes,
                "exempt_routes": exempt_routes,
            }
        )
        if code is not UNSET:
            field_dict["code"] = code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        status = d.pop("status")

        matched_routes = d.pop("matched_routes")

        exempt_routes = d.pop("exempt_routes")

        code = d.pop("code", UNSET)

        route_group_result = cls(
            name=name,
            status=status,
            matched_routes=matched_routes,
            exempt_routes=exempt_routes,
            code=code,
        )

        route_group_result.additional_properties = d
        return route_group_result

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
