from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_playbook_evidence_event import OperationWorkflowPlaybookEvidenceEvent
    from ..models.operation_workflow_readiness_snapshot_entry import OperationWorkflowReadinessSnapshotEntry


T = TypeVar("T", bound="OperationWorkflowReadinessEscalationResponseEvent")


@_attrs_define
class OperationWorkflowReadinessEscalationResponseEvent:
    """Single durable acknowledgment-deadline notice for an assigned escalation, with exact scoped source identity. Does
    not automatically schedule a second follow-up or indicate business failure.

    """

    escalation_notification_id: UUID
    """OperationWorkflowReadinessEscalationResponseEvent.escalation_notification_id. Retained acknowledgment target
    or assignment observation; never modifies application business reports."""
    escalation_sequence: int
    """OperationWorkflowReadinessEscalationResponseEvent.escalation_sequence. Retained acknowledgment target or
    assignment observation; never modifies application business reports."""
    checked_at: datetime.datetime
    """OperationWorkflowReadinessEscalationResponseEvent.checked_at. Retained acknowledgment target or assignment
    observation; never modifies application business reports."""
    deadline_at: datetime.datetime
    """OperationWorkflowReadinessEscalationResponseEvent.deadline_at. Retained acknowledgment target or assignment
    observation; never modifies application business reports."""
    owner: str
    """OperationWorkflowReadinessEscalationResponseEvent.owner. Retained acknowledgment target or assignment
    observation; never modifies application business reports."""
    reassigned: bool
    """OperationWorkflowReadinessEscalationResponseEvent.reassigned. Retained acknowledgment target or assignment
    observation; never modifies application business reports."""
    source: OperationWorkflowReadinessSnapshotEntry | Unset = UNSET
    """One saved transition identity and stable evidence summaries for comparison within the same ownership scope."""
    playbook: OperationWorkflowPlaybookEvidenceEvent | Unset = UNSET
    """OperationWorkflowPlaybookEvidenceEvent. Opt-in durable checklist evidence monitoring; advisory observations
    preserve human reviews and application business authority."""

    def to_dict(self) -> dict[str, Any]:
        escalation_notification_id = str(self.escalation_notification_id)

        escalation_sequence = self.escalation_sequence

        checked_at = self.checked_at.isoformat()

        deadline_at = self.deadline_at.isoformat()

        owner = self.owner

        reassigned = self.reassigned

        source: dict[str, Any] | Unset = UNSET
        if not isinstance(self.source, Unset):
            source = self.source.to_dict()

        playbook: dict[str, Any] | Unset = UNSET
        if not isinstance(self.playbook, Unset):
            playbook = self.playbook.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "escalation_notification_id": escalation_notification_id,
                "escalation_sequence": escalation_sequence,
                "checked_at": checked_at,
                "deadline_at": deadline_at,
                "owner": owner,
                "reassigned": reassigned,
            }
        )
        if source is not UNSET:
            field_dict["source"] = source
        if playbook is not UNSET:
            field_dict["playbook"] = playbook

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_playbook_evidence_event import OperationWorkflowPlaybookEvidenceEvent
        from ..models.operation_workflow_readiness_snapshot_entry import OperationWorkflowReadinessSnapshotEntry

        d = dict(src_dict)
        escalation_notification_id = UUID(d.pop("escalation_notification_id"))

        escalation_sequence = d.pop("escalation_sequence")

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        deadline_at = datetime.datetime.fromisoformat(d.pop("deadline_at"))

        owner = d.pop("owner")

        reassigned = d.pop("reassigned")

        _source = d.pop("source", UNSET)
        source: OperationWorkflowReadinessSnapshotEntry | Unset
        if isinstance(_source, Unset):
            source = UNSET
        else:
            source = OperationWorkflowReadinessSnapshotEntry.from_dict(_source)

        _playbook = d.pop("playbook", UNSET)
        playbook: OperationWorkflowPlaybookEvidenceEvent | Unset
        if isinstance(_playbook, Unset):
            playbook = UNSET
        else:
            playbook = OperationWorkflowPlaybookEvidenceEvent.from_dict(_playbook)

        operation_workflow_readiness_escalation_response_event = cls(
            escalation_notification_id=escalation_notification_id,
            escalation_sequence=escalation_sequence,
            checked_at=checked_at,
            deadline_at=deadline_at,
            owner=owner,
            reassigned=reassigned,
            source=source,
            playbook=playbook,
        )

        return operation_workflow_readiness_escalation_response_event
