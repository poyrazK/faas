from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.issue_ownership_rule import IssueOwnershipRule


T = TypeVar("T", bound="IssueOwnershipRules")


@_attrs_define
class IssueOwnershipRules:
    """Complete ordered app-level ownership policy. An empty rules list disables automatic assignment."""

    rules: list[IssueOwnershipRule]

    def to_dict(self) -> dict[str, Any]:
        rules = []
        for rules_item_data in self.rules:
            rules_item = rules_item_data.to_dict()
            rules.append(rules_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "rules": rules,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.issue_ownership_rule import IssueOwnershipRule

        d = dict(src_dict)
        rules = []
        _rules = d.pop("rules")
        for rules_item_data in _rules:
            rules_item = IssueOwnershipRule.from_dict(rules_item_data)

            rules.append(rules_item)

        issue_ownership_rules = cls(
            rules=rules,
        )

        return issue_ownership_rules
