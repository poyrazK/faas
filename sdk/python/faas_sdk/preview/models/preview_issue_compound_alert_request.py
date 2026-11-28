from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.issue_compound_alert_rule import IssueCompoundAlertRule


T = TypeVar("T", bound="PreviewIssueCompoundAlertRequest")


@_attrs_define
class PreviewIssueCompoundAlertRequest:
    """An unsaved compound rule and an existing issue to evaluate without notification side effects."""

    issue_id: UUID
    rule: IssueCompoundAlertRule
    """Named AND rule requiring event frequency and verified customer impact in one shared window."""

    def to_dict(self) -> dict[str, Any]:
        issue_id = str(self.issue_id)

        rule = self.rule.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "issue_id": issue_id,
                "rule": rule,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.issue_compound_alert_rule import IssueCompoundAlertRule

        d = dict(src_dict)
        issue_id = UUID(d.pop("issue_id"))

        rule = IssueCompoundAlertRule.from_dict(d.pop("rule"))

        preview_issue_compound_alert_request = cls(
            issue_id=issue_id,
            rule=rule,
        )

        return preview_issue_compound_alert_request
