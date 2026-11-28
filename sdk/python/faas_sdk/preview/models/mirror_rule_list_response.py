from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.mirror_rule_response import MirrorRuleResponse


T = TypeVar("T", bound="MirrorRuleListResponse")


@_attrs_define
class MirrorRuleListResponse:
    """Wrapper for GET /v1/apps/{slug}/mirrors. Bounded by
    `Limits.MirrorTargetsPerApp` (1-3) — no cursor in A2.

    """

    rules: list[MirrorRuleResponse]
    count: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        rules = []
        for rules_item_data in self.rules:
            rules_item = rules_item_data.to_dict()
            rules.append(rules_item)

        count = self.count

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "rules": rules,
                "count": count,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mirror_rule_response import MirrorRuleResponse

        d = dict(src_dict)
        rules = []
        _rules = d.pop("rules")
        for rules_item_data in _rules:
            rules_item = MirrorRuleResponse.from_dict(rules_item_data)

            rules.append(rules_item)

        count = d.pop("count")

        mirror_rule_list_response = cls(
            rules=rules,
            count=count,
        )

        mirror_rule_list_response.additional_properties = d
        return mirror_rule_list_response

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
