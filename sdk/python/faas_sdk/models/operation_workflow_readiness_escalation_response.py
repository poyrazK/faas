from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_readiness_escalation_response_status import (
    OperationWorkflowReadinessEscalationResponseStatus,
    check_operation_workflow_readiness_escalation_response_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_readiness_assignment_change import OperationWorkflowReadinessAssignmentChange


T = TypeVar("T", bound="OperationWorkflowReadinessEscalationResponse")


@_attrs_define
class OperationWorkflowReadinessEscalationResponse:
    """Optional acknowledgment response target on a retained assigned escalation. Human acknowledgment fulfills it;
    completion or follow-up clearing stops it. A missed target emits one notice and optional guarded fallback.
    Assignment history retains the latest 16 changes.

    """

    deadline_at: datetime.datetime
    """OperationWorkflowReadinessEscalationResponse.deadline_at. Retained acknowledgment target or assignment
    observation; never modifies application business reports."""
    initial_owner: str
    """OperationWorkflowReadinessEscalationResponse.initial_owner. Retained acknowledgment target or assignment
    observation; never modifies application business reports."""
    status: OperationWorkflowReadinessEscalationResponseStatus
    """OperationWorkflowReadinessEscalationResponse.status. Retained acknowledgment target or assignment
    observation; never modifies application business reports."""
    history: list[OperationWorkflowReadinessAssignmentChange]
    """OperationWorkflowReadinessEscalationResponse.history. Latest retained manual or automatic assignment
    changes."""
    history_truncated: bool
    """OperationWorkflowReadinessEscalationResponse.history_truncated. Retained acknowledgment target or assignment
    observation; never modifies application business reports."""
    fallback_owner: str | Unset = UNSET
    """OperationWorkflowReadinessEscalationResponse.fallback_owner. Retained acknowledgment target or assignment
    observation; never modifies application business reports."""
    first_acknowledged_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReadinessEscalationResponse.first_acknowledged_at. Retained acknowledgment target or
    assignment observation; never modifies application business reports."""
    deadline_notified_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReadinessEscalationResponse.deadline_notified_at. Retained acknowledgment target or
    assignment observation; never modifies application business reports."""

    def to_dict(self) -> dict[str, Any]:
        deadline_at = self.deadline_at.isoformat()

        initial_owner = self.initial_owner

        status: str = self.status

        history = []
        for history_item_data in self.history:
            history_item = history_item_data.to_dict()
            history.append(history_item)

        history_truncated = self.history_truncated

        fallback_owner = self.fallback_owner

        first_acknowledged_at: str | Unset = UNSET
        if not isinstance(self.first_acknowledged_at, Unset):
            first_acknowledged_at = self.first_acknowledged_at.isoformat()

        deadline_notified_at: str | Unset = UNSET
        if not isinstance(self.deadline_notified_at, Unset):
            deadline_notified_at = self.deadline_notified_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "deadline_at": deadline_at,
                "initial_owner": initial_owner,
                "status": status,
                "history": history,
                "history_truncated": history_truncated,
            }
        )
        if fallback_owner is not UNSET:
            field_dict["fallback_owner"] = fallback_owner
        if first_acknowledged_at is not UNSET:
            field_dict["first_acknowledged_at"] = first_acknowledged_at
        if deadline_notified_at is not UNSET:
            field_dict["deadline_notified_at"] = deadline_notified_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_readiness_assignment_change import OperationWorkflowReadinessAssignmentChange

        d = dict(src_dict)
        deadline_at = datetime.datetime.fromisoformat(d.pop("deadline_at"))

        initial_owner = d.pop("initial_owner")

        status = check_operation_workflow_readiness_escalation_response_status(d.pop("status"))

        history = []
        _history = d.pop("history")
        for history_item_data in _history:
            history_item = OperationWorkflowReadinessAssignmentChange.from_dict(history_item_data)

            history.append(history_item)

        history_truncated = d.pop("history_truncated")

        fallback_owner = d.pop("fallback_owner", UNSET)

        _first_acknowledged_at = d.pop("first_acknowledged_at", UNSET)
        first_acknowledged_at: datetime.datetime | Unset
        if isinstance(_first_acknowledged_at, Unset):
            first_acknowledged_at = UNSET
        else:
            first_acknowledged_at = datetime.datetime.fromisoformat(_first_acknowledged_at)

        _deadline_notified_at = d.pop("deadline_notified_at", UNSET)
        deadline_notified_at: datetime.datetime | Unset
        if isinstance(_deadline_notified_at, Unset):
            deadline_notified_at = UNSET
        else:
            deadline_notified_at = datetime.datetime.fromisoformat(_deadline_notified_at)

        operation_workflow_readiness_escalation_response = cls(
            deadline_at=deadline_at,
            initial_owner=initial_owner,
            status=status,
            history=history,
            history_truncated=history_truncated,
            fallback_owner=fallback_owner,
            first_acknowledged_at=first_acknowledged_at,
            deadline_notified_at=deadline_notified_at,
        )

        return operation_workflow_readiness_escalation_response
