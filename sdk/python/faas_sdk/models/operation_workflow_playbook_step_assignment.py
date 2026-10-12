from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_playbook_step_assignment_status import (
    OperationWorkflowPlaybookStepAssignmentStatus,
    check_operation_workflow_playbook_step_assignment_status,
)

if TYPE_CHECKING:
    from ..models.operation_workflow_readiness_assignment_change import OperationWorkflowReadinessAssignmentChange


T = TypeVar("T", bound="OperationWorkflowPlaybookStepAssignment")


@_attrs_define
class OperationWorkflowPlaybookStepAssignment:
    """OperationWorkflowPlaybookStepAssignment. Assigned step attention in the existing inbox and follow-up queue. Polling
    preserves manual coordination and bounded ownership history.

    """

    progress_id: UUID
    """OperationWorkflowPlaybookStepAssignment.progress_id. Exact pinned checklist identity."""
    outcome_code: str
    """OperationWorkflowPlaybookStepAssignment.outcome_code. Exact outcome follow-up selector."""
    item_index: int
    """OperationWorkflowPlaybookStepAssignment.item_index. Local step index."""
    label: str
    """OperationWorkflowPlaybookStepAssignment.label. Pinned human follow-up label."""
    status: OperationWorkflowPlaybookStepAssignmentStatus
    """OperationWorkflowPlaybookStepAssignment.status. Latest accepted human step review; independent of inbox
    completion."""
    due_at: datetime.datetime
    """OperationWorkflowPlaybookStepAssignment.due_at. Original immutable completion deadline."""
    last_step_owner: str
    """OperationWorkflowPlaybookStepAssignment.last_step_owner. Latest synchronized checklist assignment, distinct
    from a manual queue override."""
    manual_owner: bool
    """OperationWorkflowPlaybookStepAssignment.manual_owner. Any manual inbox ownership change prevents later
    checklist synchronization from overwriting it."""
    history: list[OperationWorkflowReadinessAssignmentChange]
    """OperationWorkflowPlaybookStepAssignment.history. Bounded retained checklist and inbox assignment changes."""
    history_truncated: bool
    """OperationWorkflowPlaybookStepAssignment.history_truncated. Older ownership changes are no longer retained."""

    def to_dict(self) -> dict[str, Any]:
        progress_id = str(self.progress_id)

        outcome_code = self.outcome_code

        item_index = self.item_index

        label = self.label

        status: str = self.status

        due_at = self.due_at.isoformat()

        last_step_owner = self.last_step_owner

        manual_owner = self.manual_owner

        history = []
        for history_item_data in self.history:
            history_item = history_item_data.to_dict()
            history.append(history_item)

        history_truncated = self.history_truncated

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "progress_id": progress_id,
                "outcome_code": outcome_code,
                "item_index": item_index,
                "label": label,
                "status": status,
                "due_at": due_at,
                "last_step_owner": last_step_owner,
                "manual_owner": manual_owner,
                "history": history,
                "history_truncated": history_truncated,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_readiness_assignment_change import OperationWorkflowReadinessAssignmentChange

        d = dict(src_dict)
        progress_id = UUID(d.pop("progress_id"))

        outcome_code = d.pop("outcome_code")

        item_index = d.pop("item_index")

        label = d.pop("label")

        status = check_operation_workflow_playbook_step_assignment_status(d.pop("status"))

        due_at = datetime.datetime.fromisoformat(d.pop("due_at"))

        last_step_owner = d.pop("last_step_owner")

        manual_owner = d.pop("manual_owner")

        history = []
        _history = d.pop("history")
        for history_item_data in _history:
            history_item = OperationWorkflowReadinessAssignmentChange.from_dict(history_item_data)

            history.append(history_item)

        history_truncated = d.pop("history_truncated")

        operation_workflow_playbook_step_assignment = cls(
            progress_id=progress_id,
            outcome_code=outcome_code,
            item_index=item_index,
            label=label,
            status=status,
            due_at=due_at,
            last_step_owner=last_step_owner,
            manual_owner=manual_owner,
            history=history,
            history_truncated=history_truncated,
        )

        return operation_workflow_playbook_step_assignment
