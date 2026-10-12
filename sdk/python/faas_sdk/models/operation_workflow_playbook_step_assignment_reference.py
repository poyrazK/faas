from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_readiness_outcome_escalation_reference import (
        OperationWorkflowReadinessOutcomeEscalationReference,
    )


T = TypeVar("T", bound="OperationWorkflowPlaybookStepAssignmentReference")


@_attrs_define
class OperationWorkflowPlaybookStepAssignmentReference:
    """OperationWorkflowPlaybookStepAssignmentReference. One retained queue delivery identity per outcome step and watch."""

    outcome_code: str
    """OperationWorkflowPlaybookStepAssignmentReference.outcome_code. Pinned outcome selector."""
    item_index: int
    """OperationWorkflowPlaybookStepAssignmentReference.item_index. Exact local step index."""
    notification: OperationWorkflowReadinessOutcomeEscalationReference
    """Reference to the single retained escalation notification in the same subscription. Pruning does not recreate
    escalations; links may expire."""

    def to_dict(self) -> dict[str, Any]:
        outcome_code = self.outcome_code

        item_index = self.item_index

        notification = self.notification.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "outcome_code": outcome_code,
                "item_index": item_index,
                "notification": notification,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_readiness_outcome_escalation_reference import (
            OperationWorkflowReadinessOutcomeEscalationReference,
        )

        d = dict(src_dict)
        outcome_code = d.pop("outcome_code")

        item_index = d.pop("item_index")

        notification = OperationWorkflowReadinessOutcomeEscalationReference.from_dict(d.pop("notification"))

        operation_workflow_playbook_step_assignment_reference = cls(
            outcome_code=outcome_code,
            item_index=item_index,
            notification=notification,
        )

        return operation_workflow_playbook_step_assignment_reference
