from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_readiness_business_outcome_event_previous_status import (
    OperationWorkflowReadinessBusinessOutcomeEventPreviousStatus,
    check_operation_workflow_readiness_business_outcome_event_previous_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_readiness_business_outcome import OperationWorkflowReadinessBusinessOutcome
    from ..models.operation_workflow_readiness_outcome_escalation_reference import (
        OperationWorkflowReadinessOutcomeEscalationReference,
    )
    from ..models.operation_workflow_readiness_snapshot_entry import OperationWorkflowReadinessSnapshotEntry


T = TypeVar("T", bound="OperationWorkflowReadinessBusinessOutcomeEvent")


@_attrs_define
class OperationWorkflowReadinessBusinessOutcomeEvent:
    """Self-contained observed business result or deadline notice for a source completion. Reports never establish
    causation, business failure or execution authority. Source links may expire from inbox retention.

    """

    watch_id: UUID
    """OperationWorkflowReadinessBusinessOutcomeEvent.watch_id. Retained advisory business outcome watch metadata;
    does not modify application reports or coordination revisions."""
    source_notification_id: UUID
    """OperationWorkflowReadinessBusinessOutcomeEvent.source_notification_id. Retained advisory business outcome
    watch metadata; does not modify application reports or coordination revisions."""
    source_sequence: int
    """OperationWorkflowReadinessBusinessOutcomeEvent.source_sequence. Retained advisory business outcome watch
    metadata; does not modify application reports or coordination revisions."""
    source: OperationWorkflowReadinessSnapshotEntry
    """One saved transition identity and stable evidence summaries for comparison within the same ownership scope."""
    previous_status: OperationWorkflowReadinessBusinessOutcomeEventPreviousStatus
    """OperationWorkflowReadinessBusinessOutcomeEvent.previous_status. Retained advisory business outcome watch
    metadata; does not modify application reports or coordination revisions."""
    current: OperationWorkflowReadinessBusinessOutcome
    """Advisory comparison of the expected terminal outcome with a scoped retained application report. Missing
    state, stale reports, contract changes, older revisions and terminal reports without outcome codes remain
    unknown. Explicit outcome codes do not imply causation or authorize execution. Completion snapshots are
    immutable; current checks are live reads."""
    escalation: OperationWorkflowReadinessOutcomeEscalationReference | Unset = UNSET
    """Reference to the single retained escalation notification in the same subscription. Pruning does not recreate
    escalations; links may expire."""
    deadline_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReadinessBusinessOutcomeEvent.deadline_at. Retained advisory business outcome watch
    metadata; does not modify application reports or coordination revisions."""

    def to_dict(self) -> dict[str, Any]:
        watch_id = str(self.watch_id)

        source_notification_id = str(self.source_notification_id)

        source_sequence = self.source_sequence

        source = self.source.to_dict()

        previous_status: str = self.previous_status

        current = self.current.to_dict()

        escalation: dict[str, Any] | Unset = UNSET
        if not isinstance(self.escalation, Unset):
            escalation = self.escalation.to_dict()

        deadline_at: str | Unset = UNSET
        if not isinstance(self.deadline_at, Unset):
            deadline_at = self.deadline_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "watch_id": watch_id,
                "source_notification_id": source_notification_id,
                "source_sequence": source_sequence,
                "source": source,
                "previous_status": previous_status,
                "current": current,
            }
        )
        if escalation is not UNSET:
            field_dict["escalation"] = escalation
        if deadline_at is not UNSET:
            field_dict["deadline_at"] = deadline_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_readiness_business_outcome import OperationWorkflowReadinessBusinessOutcome
        from ..models.operation_workflow_readiness_outcome_escalation_reference import (
            OperationWorkflowReadinessOutcomeEscalationReference,
        )
        from ..models.operation_workflow_readiness_snapshot_entry import OperationWorkflowReadinessSnapshotEntry

        d = dict(src_dict)
        watch_id = UUID(d.pop("watch_id"))

        source_notification_id = UUID(d.pop("source_notification_id"))

        source_sequence = d.pop("source_sequence")

        source = OperationWorkflowReadinessSnapshotEntry.from_dict(d.pop("source"))

        previous_status = check_operation_workflow_readiness_business_outcome_event_previous_status(
            d.pop("previous_status")
        )

        current = OperationWorkflowReadinessBusinessOutcome.from_dict(d.pop("current"))

        _escalation = d.pop("escalation", UNSET)
        escalation: OperationWorkflowReadinessOutcomeEscalationReference | Unset
        if isinstance(_escalation, Unset):
            escalation = UNSET
        else:
            escalation = OperationWorkflowReadinessOutcomeEscalationReference.from_dict(_escalation)

        _deadline_at = d.pop("deadline_at", UNSET)
        deadline_at: datetime.datetime | Unset
        if isinstance(_deadline_at, Unset):
            deadline_at = UNSET
        else:
            deadline_at = datetime.datetime.fromisoformat(_deadline_at)

        operation_workflow_readiness_business_outcome_event = cls(
            watch_id=watch_id,
            source_notification_id=source_notification_id,
            source_sequence=source_sequence,
            source=source,
            previous_status=previous_status,
            current=current,
            escalation=escalation,
            deadline_at=deadline_at,
        )

        return operation_workflow_readiness_business_outcome_event
