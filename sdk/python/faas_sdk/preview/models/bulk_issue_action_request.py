from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.bulk_issue_lifecycle_action import BulkIssueLifecycleAction


T = TypeVar("T", bound="BulkIssueActionRequest")


@_attrs_define
class BulkIssueActionRequest:
    """Bounded issue selection and one lifecycle or assignment operation to apply atomically."""

    issue_ids: list[UUID]
    action: BulkIssueLifecycleAction
    """Lifecycle or assignment action applied atomically to the selected canonical issue groups."""

    def to_dict(self) -> dict[str, Any]:
        issue_ids = []
        for issue_ids_item_data in self.issue_ids:
            issue_ids_item = str(issue_ids_item_data)
            issue_ids.append(issue_ids_item)

        action = self.action.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "issue_ids": issue_ids,
                "action": action,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.bulk_issue_lifecycle_action import BulkIssueLifecycleAction

        d = dict(src_dict)
        issue_ids = []
        _issue_ids = d.pop("issue_ids")
        for issue_ids_item_data in _issue_ids:
            issue_ids_item = UUID(issue_ids_item_data)

            issue_ids.append(issue_ids_item)

        action = BulkIssueLifecycleAction.from_dict(d.pop("action"))

        bulk_issue_action_request = cls(
            issue_ids=issue_ids,
            action=action,
        )

        return bulk_issue_action_request
