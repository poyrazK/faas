from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="IssueSplitRuleUndoRequest")


@_attrs_define
class IssueSplitRuleUndoRequest:
    """Reviewed canonical destination required for atomic split-rule undo; a changed destination rejects the operation
    without disabling the rule.

    """

    canonical_source_issue_id: UUID

    def to_dict(self) -> dict[str, Any]:
        canonical_source_issue_id = str(self.canonical_source_issue_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "canonical_source_issue_id": canonical_source_issue_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        canonical_source_issue_id = UUID(d.pop("canonical_source_issue_id"))

        issue_split_rule_undo_request = cls(
            canonical_source_issue_id=canonical_source_issue_id,
        )

        return issue_split_rule_undo_request
