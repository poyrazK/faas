from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_recovery_automatic_action_status import (
    WorkflowRecoveryAutomaticActionStatus,
    check_workflow_recovery_automatic_action_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_recovery_automatic_blocked_notification import WorkflowRecoveryAutomaticBlockedNotification
    from ..models.workflow_recovery_automatic_evaluation import WorkflowRecoveryAutomaticEvaluation
    from ..models.workflow_recovery_automatic_suppression import WorkflowRecoveryAutomaticSuppression
    from ..models.workflow_recovery_review_action_status import WorkflowRecoveryReviewActionStatus


T = TypeVar("T", bound="WorkflowRecoveryAutomaticAction")


@_attrs_define
class WorkflowRecoveryAutomaticAction:
    """Durable automatic retry intent and application receipt. Applied identifies child creation; its final result requires
    retained generation proof. Suppressed actions never run automatically.

    """

    action_id: UUID
    position: int
    status: WorkflowRecoveryAutomaticActionStatus
    reason: str
    last_evaluation: WorkflowRecoveryAutomaticEvaluation | Unset = UNSET
    """Last committed worker evaluation, never refreshed by reads or notification capture. Waiting_since is the
    first observation of this consecutive wait reason and resets when the reason changes. Absent for legacy or
    unevaluated intents. Pausing preserves historical evidence."""
    suppression: WorkflowRecoveryAutomaticSuppression | Unset = UNSET
    """Immutable operator suppression audit retained with the automatic retry intent."""
    blocked_notification: WorkflowRecoveryAutomaticBlockedNotification | Unset = UNSET
    """Latest blocked or recovered evidence capture for a retry or initial-recovery review. Retry sequence
    increases only on a changed evidence diagnosis or proven clearance. Receivers are frozen at capture; queued
    identifies ledger admission rather than successful delivery. No receivers prevents historical replay to later
    subscribers."""
    child_job_id: UUID | Unset = UNSET
    applied_at: datetime.datetime | Unset = UNSET
    result: WorkflowRecoveryReviewActionStatus | Unset = UNSET
    """Application freshness and execution outcome for one frozen action. A retry is complete only with saved
    terminal generation evidence or a terminal admission refusal."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        action_id = str(self.action_id)

        position = self.position

        status: str = self.status

        reason = self.reason

        last_evaluation: dict[str, Any] | Unset = UNSET
        if not isinstance(self.last_evaluation, Unset):
            last_evaluation = self.last_evaluation.to_dict()

        suppression: dict[str, Any] | Unset = UNSET
        if not isinstance(self.suppression, Unset):
            suppression = self.suppression.to_dict()

        blocked_notification: dict[str, Any] | Unset = UNSET
        if not isinstance(self.blocked_notification, Unset):
            blocked_notification = self.blocked_notification.to_dict()

        child_job_id: str | Unset = UNSET
        if not isinstance(self.child_job_id, Unset):
            child_job_id = str(self.child_job_id)

        applied_at: str | Unset = UNSET
        if not isinstance(self.applied_at, Unset):
            applied_at = self.applied_at.isoformat()

        result: dict[str, Any] | Unset = UNSET
        if not isinstance(self.result, Unset):
            result = self.result.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "action_id": action_id,
                "position": position,
                "status": status,
                "reason": reason,
            }
        )
        if last_evaluation is not UNSET:
            field_dict["last_evaluation"] = last_evaluation
        if suppression is not UNSET:
            field_dict["suppression"] = suppression
        if blocked_notification is not UNSET:
            field_dict["blocked_notification"] = blocked_notification
        if child_job_id is not UNSET:
            field_dict["child_job_id"] = child_job_id
        if applied_at is not UNSET:
            field_dict["applied_at"] = applied_at
        if result is not UNSET:
            field_dict["result"] = result

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_recovery_automatic_blocked_notification import (
            WorkflowRecoveryAutomaticBlockedNotification,
        )
        from ..models.workflow_recovery_automatic_evaluation import WorkflowRecoveryAutomaticEvaluation
        from ..models.workflow_recovery_automatic_suppression import WorkflowRecoveryAutomaticSuppression
        from ..models.workflow_recovery_review_action_status import WorkflowRecoveryReviewActionStatus

        d = dict(src_dict)
        action_id = UUID(d.pop("action_id"))

        position = d.pop("position")

        status = check_workflow_recovery_automatic_action_status(d.pop("status"))

        reason = d.pop("reason")

        _last_evaluation = d.pop("last_evaluation", UNSET)
        last_evaluation: WorkflowRecoveryAutomaticEvaluation | Unset
        if isinstance(_last_evaluation, Unset):
            last_evaluation = UNSET
        else:
            last_evaluation = WorkflowRecoveryAutomaticEvaluation.from_dict(_last_evaluation)

        _suppression = d.pop("suppression", UNSET)
        suppression: WorkflowRecoveryAutomaticSuppression | Unset
        if isinstance(_suppression, Unset):
            suppression = UNSET
        else:
            suppression = WorkflowRecoveryAutomaticSuppression.from_dict(_suppression)

        _blocked_notification = d.pop("blocked_notification", UNSET)
        blocked_notification: WorkflowRecoveryAutomaticBlockedNotification | Unset
        if isinstance(_blocked_notification, Unset):
            blocked_notification = UNSET
        else:
            blocked_notification = WorkflowRecoveryAutomaticBlockedNotification.from_dict(_blocked_notification)

        _child_job_id = d.pop("child_job_id", UNSET)
        child_job_id: UUID | Unset
        if isinstance(_child_job_id, Unset):
            child_job_id = UNSET
        else:
            child_job_id = UUID(_child_job_id)

        _applied_at = d.pop("applied_at", UNSET)
        applied_at: datetime.datetime | Unset
        if isinstance(_applied_at, Unset):
            applied_at = UNSET
        else:
            applied_at = datetime.datetime.fromisoformat(_applied_at)

        _result = d.pop("result", UNSET)
        result: WorkflowRecoveryReviewActionStatus | Unset
        if isinstance(_result, Unset):
            result = UNSET
        else:
            result = WorkflowRecoveryReviewActionStatus.from_dict(_result)

        workflow_recovery_automatic_action = cls(
            action_id=action_id,
            position=position,
            status=status,
            reason=reason,
            last_evaluation=last_evaluation,
            suppression=suppression,
            blocked_notification=blocked_notification,
            child_job_id=child_job_id,
            applied_at=applied_at,
            result=result,
        )

        workflow_recovery_automatic_action.additional_properties = d
        return workflow_recovery_automatic_action

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
