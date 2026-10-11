from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_recovery_execution_finished_webhook_payload_mode import (
    EventRecoveryExecutionFinishedWebhookPayloadMode,
    check_event_recovery_execution_finished_webhook_payload_mode,
)
from ..models.event_recovery_execution_finished_webhook_payload_outcome import (
    EventRecoveryExecutionFinishedWebhookPayloadOutcome,
    check_event_recovery_execution_finished_webhook_payload_outcome,
)
from ..models.event_recovery_execution_finished_webhook_payload_pending_count import (
    EventRecoveryExecutionFinishedWebhookPayloadPendingCount,
    check_event_recovery_execution_finished_webhook_payload_pending_count,
)
from ..models.event_recovery_execution_finished_webhook_payload_state import (
    EventRecoveryExecutionFinishedWebhookPayloadState,
    check_event_recovery_execution_finished_webhook_payload_state,
)
from ..models.event_recovery_execution_finished_webhook_payload_unresolved_count import (
    EventRecoveryExecutionFinishedWebhookPayloadUnresolvedCount,
    check_event_recovery_execution_finished_webhook_payload_unresolved_count,
)

if TYPE_CHECKING:
    from ..models.event_recovery_execution_summary import EventRecoveryExecutionSummary


T = TypeVar("T", bound="EventRecoveryExecutionFinishedWebhookPayload")


@_attrs_define
class EventRecoveryExecutionFinishedWebhookPayload:
    """Frozen confirmed terminal execution results for all queued deliveries after admission finishes. Unknown outcomes
    block capture. Separate from event_recovery.completed; does not imply every execution succeeded or any receiver
    acknowledged.

    """

    event_id: UUID
    job_id: UUID
    app_id: UUID
    mode: EventRecoveryExecutionFinishedWebhookPayloadMode
    state: EventRecoveryExecutionFinishedWebhookPayloadState
    outcome: EventRecoveryExecutionFinishedWebhookPayloadOutcome
    selected_count: int
    pending_count: EventRecoveryExecutionFinishedWebhookPayloadPendingCount
    queued_count: int
    skipped_count: int
    cancelled_count: int
    created_at: datetime.datetime
    expires_at: datetime.datetime
    completed_at: datetime.datetime
    execution: EventRecoveryExecutionSummary
    """Observations of admitted execution-mode items, preferring saved terminal results over live records. Legacy
    admissions without evidence remain unknown. Counts sum to tracked_count and are separate from job admission
    state. Omitted for routing recovery. Saved results share recovery job retention."""
    execution_finished_at: datetime.datetime
    unresolved_count: EventRecoveryExecutionFinishedWebhookPayloadUnresolvedCount
    """Unknown or nonterminal queued deliveries; capture requires zero."""

    def to_dict(self) -> dict[str, Any]:
        event_id = str(self.event_id)

        job_id = str(self.job_id)

        app_id = str(self.app_id)

        mode: str = self.mode

        state: str = self.state

        outcome: str = self.outcome

        selected_count = self.selected_count

        pending_count: int = self.pending_count

        queued_count = self.queued_count

        skipped_count = self.skipped_count

        cancelled_count = self.cancelled_count

        created_at = self.created_at.isoformat()

        expires_at = self.expires_at.isoformat()

        completed_at = self.completed_at.isoformat()

        execution = self.execution.to_dict()

        execution_finished_at = self.execution_finished_at.isoformat()

        unresolved_count: int = self.unresolved_count

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "event_id": event_id,
                "job_id": job_id,
                "app_id": app_id,
                "mode": mode,
                "state": state,
                "outcome": outcome,
                "selected_count": selected_count,
                "pending_count": pending_count,
                "queued_count": queued_count,
                "skipped_count": skipped_count,
                "cancelled_count": cancelled_count,
                "created_at": created_at,
                "expires_at": expires_at,
                "completed_at": completed_at,
                "execution": execution,
                "execution_finished_at": execution_finished_at,
                "unresolved_count": unresolved_count,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_recovery_execution_summary import EventRecoveryExecutionSummary

        d = dict(src_dict)
        event_id = UUID(d.pop("event_id"))

        job_id = UUID(d.pop("job_id"))

        app_id = UUID(d.pop("app_id"))

        mode = check_event_recovery_execution_finished_webhook_payload_mode(d.pop("mode"))

        state = check_event_recovery_execution_finished_webhook_payload_state(d.pop("state"))

        outcome = check_event_recovery_execution_finished_webhook_payload_outcome(d.pop("outcome"))

        selected_count = d.pop("selected_count")

        pending_count = check_event_recovery_execution_finished_webhook_payload_pending_count(d.pop("pending_count"))

        queued_count = d.pop("queued_count")

        skipped_count = d.pop("skipped_count")

        cancelled_count = d.pop("cancelled_count")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        completed_at = datetime.datetime.fromisoformat(d.pop("completed_at"))

        execution = EventRecoveryExecutionSummary.from_dict(d.pop("execution"))

        execution_finished_at = datetime.datetime.fromisoformat(d.pop("execution_finished_at"))

        unresolved_count = check_event_recovery_execution_finished_webhook_payload_unresolved_count(
            d.pop("unresolved_count")
        )

        event_recovery_execution_finished_webhook_payload = cls(
            event_id=event_id,
            job_id=job_id,
            app_id=app_id,
            mode=mode,
            state=state,
            outcome=outcome,
            selected_count=selected_count,
            pending_count=pending_count,
            queued_count=queued_count,
            skipped_count=skipped_count,
            cancelled_count=cancelled_count,
            created_at=created_at,
            expires_at=expires_at,
            completed_at=completed_at,
            execution=execution,
            execution_finished_at=execution_finished_at,
            unresolved_count=unresolved_count,
        )

        return event_recovery_execution_finished_webhook_payload
