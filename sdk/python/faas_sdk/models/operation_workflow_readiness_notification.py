from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_readiness_notification_kind import (
    OperationWorkflowReadinessNotificationKind,
    check_operation_workflow_readiness_notification_kind,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_readiness_change import OperationWorkflowReadinessChange
    from ..models.operation_workflow_readiness_coordination import OperationWorkflowReadinessCoordination
    from ..models.operation_workflow_readiness_due_reminder import OperationWorkflowReadinessDueReminder


T = TypeVar("T", bound="OperationWorkflowReadinessNotification")


@_attrs_define
class OperationWorkflowReadinessNotification:
    """Durable advisory readiness notification containing a bounded comparison and reporting Operation identities."""

    id: UUID
    """OperationWorkflowReadinessNotification.id. Stable notification UUID."""
    sequence: int
    """OperationWorkflowReadinessNotification.sequence. Monotonic subscription-local delivery cursor."""
    kind: OperationWorkflowReadinessNotificationKind
    """OperationWorkflowReadinessNotification.kind. Matched subscription notification kind."""
    created_at: datetime.datetime
    """OperationWorkflowReadinessNotification.created_at. Time the observation was converted into a notification."""
    explanation: str
    """OperationWorkflowReadinessNotification.explanation. Human-readable change or unavailable-evaluation
    explanation."""
    coordination: OperationWorkflowReadinessCoordination | Unset = UNSET
    """OperationWorkflowReadinessCoordination. Shared inbox coordination with optimistic revision checks; omission
    of a follow-up time clears it."""
    change: OperationWorkflowReadinessChange | Unset = UNSET
    """Readiness comparison of the saved edge with fresh retained evidence. Contract and state boundaries are
    explicit."""
    reminder: OperationWorkflowReadinessDueReminder | Unset = UNSET
    """One durable in-app reminder marker for the original notification and follow-up deadline; rescheduling or
    clearing retires it. Does not authorize execution."""

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        sequence = self.sequence

        kind: str = self.kind

        created_at = self.created_at.isoformat()

        explanation = self.explanation

        coordination: dict[str, Any] | Unset = UNSET
        if not isinstance(self.coordination, Unset):
            coordination = self.coordination.to_dict()

        change: dict[str, Any] | Unset = UNSET
        if not isinstance(self.change, Unset):
            change = self.change.to_dict()

        reminder: dict[str, Any] | Unset = UNSET
        if not isinstance(self.reminder, Unset):
            reminder = self.reminder.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "sequence": sequence,
                "kind": kind,
                "created_at": created_at,
                "explanation": explanation,
            }
        )
        if coordination is not UNSET:
            field_dict["coordination"] = coordination
        if change is not UNSET:
            field_dict["change"] = change
        if reminder is not UNSET:
            field_dict["reminder"] = reminder

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_readiness_change import OperationWorkflowReadinessChange
        from ..models.operation_workflow_readiness_coordination import OperationWorkflowReadinessCoordination
        from ..models.operation_workflow_readiness_due_reminder import OperationWorkflowReadinessDueReminder

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        sequence = d.pop("sequence")

        kind = check_operation_workflow_readiness_notification_kind(d.pop("kind"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        explanation = d.pop("explanation")

        _coordination = d.pop("coordination", UNSET)
        coordination: OperationWorkflowReadinessCoordination | Unset
        if isinstance(_coordination, Unset):
            coordination = UNSET
        else:
            coordination = OperationWorkflowReadinessCoordination.from_dict(_coordination)

        _change = d.pop("change", UNSET)
        change: OperationWorkflowReadinessChange | Unset
        if isinstance(_change, Unset):
            change = UNSET
        else:
            change = OperationWorkflowReadinessChange.from_dict(_change)

        _reminder = d.pop("reminder", UNSET)
        reminder: OperationWorkflowReadinessDueReminder | Unset
        if isinstance(_reminder, Unset):
            reminder = UNSET
        else:
            reminder = OperationWorkflowReadinessDueReminder.from_dict(_reminder)

        operation_workflow_readiness_notification = cls(
            id=id,
            sequence=sequence,
            kind=kind,
            created_at=created_at,
            explanation=explanation,
            coordination=coordination,
            change=change,
            reminder=reminder,
        )

        return operation_workflow_readiness_notification
