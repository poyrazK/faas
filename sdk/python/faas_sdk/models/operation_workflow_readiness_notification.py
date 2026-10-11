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
    from ..models.operation_workflow_readiness_verification_event import OperationWorkflowReadinessVerificationEvent
    from ..models.operation_workflow_readiness_verification_watch import OperationWorkflowReadinessVerificationWatch


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
    verification_watch: OperationWorkflowReadinessVerificationWatch | Unset = UNSET
    """Opt-in watch of captured resolution identities. Verified proofs remain observed for later loss or explicit
    blocker reopening; partial coverage and source inbox retention remain explicit."""
    verification: OperationWorkflowReadinessVerificationEvent | Unset = UNSET
    """Durable in-app notification linked to its original completion watch. Source describes the original
    notification edge; verified does not imply workflow readiness or execution authority."""
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

        verification_watch: dict[str, Any] | Unset = UNSET
        if not isinstance(self.verification_watch, Unset):
            verification_watch = self.verification_watch.to_dict()

        verification: dict[str, Any] | Unset = UNSET
        if not isinstance(self.verification, Unset):
            verification = self.verification.to_dict()

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
        if verification_watch is not UNSET:
            field_dict["verification_watch"] = verification_watch
        if verification is not UNSET:
            field_dict["verification"] = verification
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
        from ..models.operation_workflow_readiness_verification_event import OperationWorkflowReadinessVerificationEvent
        from ..models.operation_workflow_readiness_verification_watch import OperationWorkflowReadinessVerificationWatch

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        sequence = d.pop("sequence")

        kind = check_operation_workflow_readiness_notification_kind(d.pop("kind"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        explanation = d.pop("explanation")

        _verification_watch = d.pop("verification_watch", UNSET)
        verification_watch: OperationWorkflowReadinessVerificationWatch | Unset
        if isinstance(_verification_watch, Unset):
            verification_watch = UNSET
        else:
            verification_watch = OperationWorkflowReadinessVerificationWatch.from_dict(_verification_watch)

        _verification = d.pop("verification", UNSET)
        verification: OperationWorkflowReadinessVerificationEvent | Unset
        if isinstance(_verification, Unset):
            verification = UNSET
        else:
            verification = OperationWorkflowReadinessVerificationEvent.from_dict(_verification)

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
            verification_watch=verification_watch,
            verification=verification,
            coordination=coordination,
            change=change,
            reminder=reminder,
        )

        return operation_workflow_readiness_notification
