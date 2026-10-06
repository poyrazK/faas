from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RoutePolicyAppliedChange")


@_attrs_define
class RoutePolicyAppliedChange:
    """Committed rule operation with the real persistent rule identifier."""

    operation: str
    kind: str
    rule_id: UUID
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        operation = self.operation

        kind = self.kind

        rule_id = str(self.rule_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "operation": operation,
                "kind": kind,
                "rule_id": rule_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        operation = d.pop("operation")

        kind = d.pop("kind")

        rule_id = UUID(d.pop("rule_id"))

        route_policy_applied_change = cls(
            operation=operation,
            kind=kind,
            rule_id=rule_id,
        )

        route_policy_applied_change.additional_properties = d
        return route_policy_applied_change

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
