from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_recovery_automatic_blocked_notification_evidence_kind import (
    WorkflowRecoveryAutomaticBlockedNotificationEvidenceKind,
    check_workflow_recovery_automatic_blocked_notification_evidence_kind,
)
from ..models.workflow_recovery_automatic_blocked_notification_reason import (
    WorkflowRecoveryAutomaticBlockedNotificationReason,
    check_workflow_recovery_automatic_blocked_notification_reason,
)
from ..models.workflow_recovery_automatic_blocked_notification_state import (
    WorkflowRecoveryAutomaticBlockedNotificationState,
    check_workflow_recovery_automatic_blocked_notification_state,
)
from ..models.workflow_recovery_automatic_blocked_notification_status import (
    WorkflowRecoveryAutomaticBlockedNotificationStatus,
    check_workflow_recovery_automatic_blocked_notification_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkflowRecoveryAutomaticBlockedNotification")


@_attrs_define
class WorkflowRecoveryAutomaticBlockedNotification:
    """Latest blocked or recovered evidence capture for a retry or initial-recovery review. Retry sequence increases only
    on a changed evidence diagnosis or proven clearance. Receivers are frozen at capture; queued identifies ledger
    admission rather than successful delivery. No receivers prevents historical replay to later subscribers.

    """

    event_id: UUID
    status: WorkflowRecoveryAutomaticBlockedNotificationStatus
    reason: WorkflowRecoveryAutomaticBlockedNotificationReason
    captured_at: datetime.datetime
    receiver_webhook_ids: list[UUID]
    state: WorkflowRecoveryAutomaticBlockedNotificationState | Unset = UNSET
    sequence: int | Unset = UNSET
    evidence_kind: WorkflowRecoveryAutomaticBlockedNotificationEvidenceKind | Unset = UNSET
    diagnosis_reason: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        event_id = str(self.event_id)

        status: str = self.status

        reason: str = self.reason

        captured_at = self.captured_at.isoformat()

        receiver_webhook_ids = []
        for receiver_webhook_ids_item_data in self.receiver_webhook_ids:
            receiver_webhook_ids_item = str(receiver_webhook_ids_item_data)
            receiver_webhook_ids.append(receiver_webhook_ids_item)

        state: str | Unset = UNSET
        if not isinstance(self.state, Unset):
            state = self.state

        sequence = self.sequence

        evidence_kind: str | Unset = UNSET
        if not isinstance(self.evidence_kind, Unset):
            evidence_kind = self.evidence_kind

        diagnosis_reason = self.diagnosis_reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "event_id": event_id,
                "status": status,
                "reason": reason,
                "captured_at": captured_at,
                "receiver_webhook_ids": receiver_webhook_ids,
            }
        )
        if state is not UNSET:
            field_dict["state"] = state
        if sequence is not UNSET:
            field_dict["sequence"] = sequence
        if evidence_kind is not UNSET:
            field_dict["evidence_kind"] = evidence_kind
        if diagnosis_reason is not UNSET:
            field_dict["diagnosis_reason"] = diagnosis_reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        event_id = UUID(d.pop("event_id"))

        status = check_workflow_recovery_automatic_blocked_notification_status(d.pop("status"))

        reason = check_workflow_recovery_automatic_blocked_notification_reason(d.pop("reason"))

        captured_at = datetime.datetime.fromisoformat(d.pop("captured_at"))

        receiver_webhook_ids = []
        _receiver_webhook_ids = d.pop("receiver_webhook_ids")
        for receiver_webhook_ids_item_data in _receiver_webhook_ids:
            receiver_webhook_ids_item = UUID(receiver_webhook_ids_item_data)

            receiver_webhook_ids.append(receiver_webhook_ids_item)

        _state = d.pop("state", UNSET)
        state: WorkflowRecoveryAutomaticBlockedNotificationState | Unset
        if isinstance(_state, Unset):
            state = UNSET
        else:
            state = check_workflow_recovery_automatic_blocked_notification_state(_state)

        sequence = d.pop("sequence", UNSET)

        _evidence_kind = d.pop("evidence_kind", UNSET)
        evidence_kind: WorkflowRecoveryAutomaticBlockedNotificationEvidenceKind | Unset
        if isinstance(_evidence_kind, Unset):
            evidence_kind = UNSET
        else:
            evidence_kind = check_workflow_recovery_automatic_blocked_notification_evidence_kind(_evidence_kind)

        diagnosis_reason = d.pop("diagnosis_reason", UNSET)

        workflow_recovery_automatic_blocked_notification = cls(
            event_id=event_id,
            status=status,
            reason=reason,
            captured_at=captured_at,
            receiver_webhook_ids=receiver_webhook_ids,
            state=state,
            sequence=sequence,
            evidence_kind=evidence_kind,
            diagnosis_reason=diagnosis_reason,
        )

        workflow_recovery_automatic_blocked_notification.additional_properties = d
        return workflow_recovery_automatic_blocked_notification

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
