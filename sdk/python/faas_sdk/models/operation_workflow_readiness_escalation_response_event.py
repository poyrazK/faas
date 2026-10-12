from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
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
    source: OperationWorkflowReadinessSnapshotEntry
    """One saved transition identity and stable evidence summaries for comparison within the same ownership scope."""
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

    def to_dict(self) -> dict[str, Any]:
        escalation_notification_id = str(self.escalation_notification_id)

        escalation_sequence = self.escalation_sequence

        source = self.source.to_dict()

        checked_at = self.checked_at.isoformat()

        deadline_at = self.deadline_at.isoformat()

        owner = self.owner

        reassigned = self.reassigned

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "escalation_notification_id": escalation_notification_id,
                "escalation_sequence": escalation_sequence,
                "source": source,
                "checked_at": checked_at,
                "deadline_at": deadline_at,
                "owner": owner,
                "reassigned": reassigned,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_readiness_snapshot_entry import OperationWorkflowReadinessSnapshotEntry

        d = dict(src_dict)
        escalation_notification_id = UUID(d.pop("escalation_notification_id"))

        escalation_sequence = d.pop("escalation_sequence")

        source = OperationWorkflowReadinessSnapshotEntry.from_dict(d.pop("source"))

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        deadline_at = datetime.datetime.fromisoformat(d.pop("deadline_at"))

        owner = d.pop("owner")

        reassigned = d.pop("reassigned")

        operation_workflow_readiness_escalation_response_event = cls(
            escalation_notification_id=escalation_notification_id,
            escalation_sequence=escalation_sequence,
            source=source,
            checked_at=checked_at,
            deadline_at=deadline_at,
            owner=owner,
            reassigned=reassigned,
        )

        return operation_workflow_readiness_escalation_response_event
