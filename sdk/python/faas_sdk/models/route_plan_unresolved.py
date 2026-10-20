from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RoutePlanUnresolved")


@_attrs_define
class RoutePlanUnresolved:
    """Requirement that remains violated or unknown after simulation, with a reason and suggested action."""

    method: str
    path: str
    requirement: str
    status: str
    code: str
    reason: str
    next_action: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        method = self.method

        path = self.path

        requirement = self.requirement

        status = self.status

        code = self.code

        reason = self.reason

        next_action = self.next_action

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "method": method,
                "path": path,
                "requirement": requirement,
                "status": status,
                "code": code,
                "reason": reason,
                "next_action": next_action,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        method = d.pop("method")

        path = d.pop("path")

        requirement = d.pop("requirement")

        status = d.pop("status")

        code = d.pop("code")

        reason = d.pop("reason")

        next_action = d.pop("next_action")

        route_plan_unresolved = cls(
            method=method,
            path=path,
            requirement=requirement,
            status=status,
            code=code,
            reason=reason,
            next_action=next_action,
        )

        route_plan_unresolved.additional_properties = d
        return route_plan_unresolved

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
