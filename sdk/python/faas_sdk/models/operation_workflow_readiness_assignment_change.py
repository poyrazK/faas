from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_readiness_assignment_change_reason import (
    OperationWorkflowReadinessAssignmentChangeReason,
    check_operation_workflow_readiness_assignment_change_reason,
)

T = TypeVar("T", bound="OperationWorkflowReadinessAssignmentChange")


@_attrs_define
class OperationWorkflowReadinessAssignmentChange:
    """Retained server-recorded ownership change for an escalation response target. History is bounded and may be
    truncated; no business authority is granted.

    """

    from_owner: str
    """OperationWorkflowReadinessAssignmentChange.from_owner. Retained acknowledgment target or assignment
    observation; never modifies application business reports."""
    to_owner: str
    """OperationWorkflowReadinessAssignmentChange.to_owner. Retained acknowledgment target or assignment
    observation; never modifies application business reports."""
    at: datetime.datetime
    """OperationWorkflowReadinessAssignmentChange.at. Retained acknowledgment target or assignment observation;
    never modifies application business reports."""
    actor: str
    """OperationWorkflowReadinessAssignmentChange.actor. Retained acknowledgment target or assignment observation;
    never modifies application business reports."""
    reason: OperationWorkflowReadinessAssignmentChangeReason
    """OperationWorkflowReadinessAssignmentChange.reason. Retained acknowledgment target or assignment observation;
    never modifies application business reports."""

    def to_dict(self) -> dict[str, Any]:
        from_owner = self.from_owner

        to_owner = self.to_owner

        at = self.at.isoformat()

        actor = self.actor

        reason: str = self.reason

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "from_owner": from_owner,
                "to_owner": to_owner,
                "at": at,
                "actor": actor,
                "reason": reason,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        from_owner = d.pop("from_owner")

        to_owner = d.pop("to_owner")

        at = datetime.datetime.fromisoformat(d.pop("at"))

        actor = d.pop("actor")

        reason = check_operation_workflow_readiness_assignment_change_reason(d.pop("reason"))

        operation_workflow_readiness_assignment_change = cls(
            from_owner=from_owner,
            to_owner=to_owner,
            at=at,
            actor=actor,
            reason=reason,
        )

        return operation_workflow_readiness_assignment_change
