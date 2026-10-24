from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RoutePolicyAffectedOperation")


@_attrs_define
class RoutePolicyAffectedOperation:
    """Policy selection impact for an intersecting captured operation, including any preserved public exception."""

    method: str
    path: str
    relation: str
    public: bool | Unset = UNSET
    before_rule_id: str | Unset = UNSET
    after_rule_id: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        method = self.method

        path = self.path

        relation = self.relation

        public = self.public

        before_rule_id = self.before_rule_id

        after_rule_id = self.after_rule_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "method": method,
                "path": path,
                "relation": relation,
            }
        )
        if public is not UNSET:
            field_dict["public"] = public
        if before_rule_id is not UNSET:
            field_dict["before_rule_id"] = before_rule_id
        if after_rule_id is not UNSET:
            field_dict["after_rule_id"] = after_rule_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        method = d.pop("method")

        path = d.pop("path")

        relation = d.pop("relation")

        public = d.pop("public", UNSET)

        before_rule_id = d.pop("before_rule_id", UNSET)

        after_rule_id = d.pop("after_rule_id", UNSET)

        route_policy_affected_operation = cls(
            method=method,
            path=path,
            relation=relation,
            public=public,
            before_rule_id=before_rule_id,
            after_rule_id=after_rule_id,
        )

        route_policy_affected_operation.additional_properties = d
        return route_policy_affected_operation

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
