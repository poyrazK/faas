from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RouteRequirementsFinding")


@_attrs_define
class RouteRequirementsFinding:
    """Allowlisted configuration summary without raw credential material or unrelated rule actions."""

    requirement: str
    status: str
    code: str
    expected: str
    reason: str
    actual: str | Unset = UNSET
    rule_ids: list[str] | Unset = UNSET
    next_action: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        requirement = self.requirement

        status = self.status

        code = self.code

        expected = self.expected

        reason = self.reason

        actual = self.actual

        rule_ids: list[str] | Unset = UNSET
        if not isinstance(self.rule_ids, Unset):
            rule_ids = self.rule_ids

        next_action = self.next_action

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "requirement": requirement,
                "status": status,
                "code": code,
                "expected": expected,
                "reason": reason,
            }
        )
        if actual is not UNSET:
            field_dict["actual"] = actual
        if rule_ids is not UNSET:
            field_dict["rule_ids"] = rule_ids
        if next_action is not UNSET:
            field_dict["next_action"] = next_action

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        requirement = d.pop("requirement")

        status = d.pop("status")

        code = d.pop("code")

        expected = d.pop("expected")

        reason = d.pop("reason")

        actual = d.pop("actual", UNSET)

        rule_ids = cast(list[str], d.pop("rule_ids", UNSET))

        next_action = d.pop("next_action", UNSET)

        route_requirements_finding = cls(
            requirement=requirement,
            status=status,
            code=code,
            expected=expected,
            reason=reason,
            actual=actual,
            rule_ids=rule_ids,
            next_action=next_action,
        )

        route_requirements_finding.additional_properties = d
        return route_requirements_finding

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
