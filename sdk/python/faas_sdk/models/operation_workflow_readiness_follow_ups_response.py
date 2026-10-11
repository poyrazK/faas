from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_readiness_follow_up import OperationWorkflowReadinessFollowUp


T = TypeVar("T", bound="OperationWorkflowReadinessFollowUpsResponse")


@_attrs_define
class OperationWorkflowReadinessFollowUpsResponse:
    """Live queue across this app/environment subscription inboxes. Owner/customer/workflow/due filters apply before
    pagination. No notification edits are performed by this read.

    """

    items: list[OperationWorkflowReadinessFollowUp]
    """OperationWorkflowReadinessFollowUpsResponse.items. Pending follow-ups ordered by due time, subscription UUID
    and sequence."""
    evaluated_at: datetime.datetime
    """OperationWorkflowReadinessFollowUpsResponse.evaluated_at. Shared clock for overdue classification."""
    total_count: int
    """OperationWorkflowReadinessFollowUpsResponse.total_count. Count or continuation offset within the selected
    retained follow-ups."""
    overdue_count: int
    """OperationWorkflowReadinessFollowUpsResponse.overdue_count. Count or continuation offset within the selected
    retained follow-ups."""
    next_offset: int
    """OperationWorkflowReadinessFollowUpsResponse.next_offset. Count or continuation offset within the selected
    retained follow-ups."""
    has_more: bool
    """OperationWorkflowReadinessFollowUpsResponse.has_more. More matching retained rows follow this live page."""
    retention_limited: bool
    """OperationWorkflowReadinessFollowUpsResponse.retention_limited. True: coordination only survives within each
    subscription latest-100 notification window; this is not an exhaustive task inventory."""
    due_reminder_count: int
    """OperationWorkflowReadinessFollowUpsResponse.due_reminder_count. Active published reminders across all
    matching retained rows, before pagination. Overdue work may await the next bounded publisher pass."""

    def to_dict(self) -> dict[str, Any]:
        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        evaluated_at = self.evaluated_at.isoformat()

        total_count = self.total_count

        overdue_count = self.overdue_count

        next_offset = self.next_offset

        has_more = self.has_more

        retention_limited = self.retention_limited

        due_reminder_count = self.due_reminder_count

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "items": items,
                "evaluated_at": evaluated_at,
                "total_count": total_count,
                "overdue_count": overdue_count,
                "next_offset": next_offset,
                "has_more": has_more,
                "retention_limited": retention_limited,
                "due_reminder_count": due_reminder_count,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_readiness_follow_up import OperationWorkflowReadinessFollowUp

        d = dict(src_dict)
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = OperationWorkflowReadinessFollowUp.from_dict(items_item_data)

            items.append(items_item)

        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        total_count = d.pop("total_count")

        overdue_count = d.pop("overdue_count")

        next_offset = d.pop("next_offset")

        has_more = d.pop("has_more")

        retention_limited = d.pop("retention_limited")

        due_reminder_count = d.pop("due_reminder_count")

        operation_workflow_readiness_follow_ups_response = cls(
            items=items,
            evaluated_at=evaluated_at,
            total_count=total_count,
            overdue_count=overdue_count,
            next_offset=next_offset,
            has_more=has_more,
            retention_limited=retention_limited,
            due_reminder_count=due_reminder_count,
        )

        return operation_workflow_readiness_follow_ups_response
