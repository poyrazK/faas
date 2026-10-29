from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_readiness_follow_ups_response_view import (
    OperationWorkflowReadinessFollowUpsResponseView,
    check_operation_workflow_readiness_follow_ups_response_view,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_next_action_queue_item import OperationWorkflowNextActionQueueItem
    from ..models.operation_workflow_readiness_follow_up import OperationWorkflowReadinessFollowUp
    from ..models.operation_workflow_readiness_impact_summary import OperationWorkflowReadinessImpactSummary


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
    view: OperationWorkflowReadinessFollowUpsResponseView | Unset = UNSET
    """Present for the next-action view; items remains empty and pagination/counts describe next_actions."""
    next_actions: list[OperationWorkflowNextActionQueueItem] | Unset = UNSET
    business_impact_summary: list[OperationWorkflowReadinessImpactSummary] | Unset = UNSET
    """OperationWorkflowReadinessFollowUpsResponse.business_impact_summary. Matching retained unresolved review
    tasks grouped by workflow and protected result before pagination. Unique obligations and cases are deduplicated
    across inbox deliveries; owner counts describe queue tasks. Ordered by critical overdue reviews, critical
    reviews, total unique reviews and stable group identity. Existing filters and candidate-window limits apply."""

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

        view: str | Unset = UNSET
        if not isinstance(self.view, Unset):
            view = self.view

        next_actions: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.next_actions, Unset):
            next_actions = []
            for next_actions_item_data in self.next_actions:
                next_actions_item = next_actions_item_data.to_dict()
                next_actions.append(next_actions_item)

        business_impact_summary: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.business_impact_summary, Unset):
            business_impact_summary = []
            for business_impact_summary_item_data in self.business_impact_summary:
                business_impact_summary_item = business_impact_summary_item_data.to_dict()
                business_impact_summary.append(business_impact_summary_item)

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
        if view is not UNSET:
            field_dict["view"] = view
        if next_actions is not UNSET:
            field_dict["next_actions"] = next_actions
        if business_impact_summary is not UNSET:
            field_dict["business_impact_summary"] = business_impact_summary

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_next_action_queue_item import OperationWorkflowNextActionQueueItem
        from ..models.operation_workflow_readiness_follow_up import OperationWorkflowReadinessFollowUp
        from ..models.operation_workflow_readiness_impact_summary import OperationWorkflowReadinessImpactSummary

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

        _view = d.pop("view", UNSET)
        view: OperationWorkflowReadinessFollowUpsResponseView | Unset
        if isinstance(_view, Unset):
            view = UNSET
        else:
            view = check_operation_workflow_readiness_follow_ups_response_view(_view)

        _next_actions = d.pop("next_actions", UNSET)
        next_actions: list[OperationWorkflowNextActionQueueItem] | Unset = UNSET
        if _next_actions is not UNSET:
            next_actions = []
            for next_actions_item_data in _next_actions:
                next_actions_item = OperationWorkflowNextActionQueueItem.from_dict(next_actions_item_data)

                next_actions.append(next_actions_item)

        _business_impact_summary = d.pop("business_impact_summary", UNSET)
        business_impact_summary: list[OperationWorkflowReadinessImpactSummary] | Unset = UNSET
        if _business_impact_summary is not UNSET:
            business_impact_summary = []
            for business_impact_summary_item_data in _business_impact_summary:
                business_impact_summary_item = OperationWorkflowReadinessImpactSummary.from_dict(
                    business_impact_summary_item_data
                )

                business_impact_summary.append(business_impact_summary_item)

        operation_workflow_readiness_follow_ups_response = cls(
            items=items,
            evaluated_at=evaluated_at,
            total_count=total_count,
            overdue_count=overdue_count,
            next_offset=next_offset,
            has_more=has_more,
            retention_limited=retention_limited,
            due_reminder_count=due_reminder_count,
            view=view,
            next_actions=next_actions,
            business_impact_summary=business_impact_summary,
        )

        return operation_workflow_readiness_follow_ups_response
