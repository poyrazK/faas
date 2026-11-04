from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationWorkflowReadinessOutcomeEscalationReference")


@_attrs_define
class OperationWorkflowReadinessOutcomeEscalationReference:
    """Reference to the single retained escalation notification in the same subscription. Pruning does not recreate
    escalations; links may expire.

    """

    notification_id: UUID
    """OperationWorkflowReadinessOutcomeEscalationReference.notification_id. Retained coordination escalation
    policy or evidence; business interpretation and execution remain application-owned."""
    sequence: int
    """OperationWorkflowReadinessOutcomeEscalationReference.sequence. Retained coordination escalation policy or
    evidence; business interpretation and execution remain application-owned."""
    created_at: datetime.datetime
    """OperationWorkflowReadinessOutcomeEscalationReference.created_at. Retained coordination escalation policy or
    evidence; business interpretation and execution remain application-owned."""

    def to_dict(self) -> dict[str, Any]:
        notification_id = str(self.notification_id)

        sequence = self.sequence

        created_at = self.created_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "notification_id": notification_id,
                "sequence": sequence,
                "created_at": created_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        notification_id = UUID(d.pop("notification_id"))

        sequence = d.pop("sequence")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        operation_workflow_readiness_outcome_escalation_reference = cls(
            notification_id=notification_id,
            sequence=sequence,
            created_at=created_at,
        )

        return operation_workflow_readiness_outcome_escalation_reference
