from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.issue_frequency_alert_rule import IssueFrequencyAlertRule


T = TypeVar("T", bound="IssueFrequencyAlertPolicy")


@_attrs_define
class IssueFrequencyAlertPolicy:
    """Current revision and complete ordered set of application frequency-alert rules."""

    revision: int
    rules: list[IssueFrequencyAlertRule]

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
        from ..models.issue_frequency_alert_rule import IssueFrequencyAlertRule

        d = dict(src_dict)
        revision = d.pop("revision")

        rules = []
        _rules = d.pop("rules")
        for rules_item_data in _rules:
            rules_item = IssueFrequencyAlertRule.from_dict(rules_item_data)

            rules.append(rules_item)

        issue_frequency_alert_policy = cls(
            revision=revision,
            rules=rules,
        )

        return issue_frequency_alert_policy
