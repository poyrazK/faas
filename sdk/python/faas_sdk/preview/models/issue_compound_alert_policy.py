from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.issue_compound_alert_rule import IssueCompoundAlertRule


T = TypeVar("T", bound="IssueCompoundAlertPolicy")


@_attrs_define
class IssueCompoundAlertPolicy:
    """Current revision and complete ordered set of application compound-alert rules."""

    revision: int
    rules: list[IssueCompoundAlertRule]

    def to_dict(self) -> dict[str, Any]:
        revision = self.revision

        rules = []
        for rules_item_data in self.rules:
            rules_item = rules_item_data.to_dict()
            rules.append(rules_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "revision": revision,
                "rules": rules,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.issue_compound_alert_rule import IssueCompoundAlertRule

        d = dict(src_dict)
        revision = d.pop("revision")

        rules = []
        _rules = d.pop("rules")
        for rules_item_data in _rules:
            rules_item = IssueCompoundAlertRule.from_dict(rules_item_data)

            rules.append(rules_item)

        issue_compound_alert_policy = cls(
            revision=revision,
            rules=rules,
        )

        return issue_compound_alert_policy
