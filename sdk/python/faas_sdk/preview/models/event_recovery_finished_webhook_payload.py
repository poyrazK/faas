from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_recovery_finished_webhook_payload_mode import (
    EventRecoveryFinishedWebhookPayloadMode,
    check_event_recovery_finished_webhook_payload_mode,
)
from ..models.event_recovery_finished_webhook_payload_outcome import (
    EventRecoveryFinishedWebhookPayloadOutcome,
    check_event_recovery_finished_webhook_payload_outcome,
)
from ..models.event_recovery_finished_webhook_payload_pending_count import (
    EventRecoveryFinishedWebhookPayloadPendingCount,
    check_event_recovery_finished_webhook_payload_pending_count,
)
from ..models.event_recovery_finished_webhook_payload_state import (
    EventRecoveryFinishedWebhookPayloadState,
    check_event_recovery_finished_webhook_payload_state,
)

T = TypeVar("T", bound="EventRecoveryFinishedWebhookPayload")


@_attrs_define
class EventRecoveryFinishedWebhookPayload:
    """Frozen terminal recovery admission metadata. Completed means recovery admission finished; admitted handler
    executions and routing retries can continue. Expired uses stored cancelled state with distinct expired outcome. JSON
    delivery nests this payload under payload.data; CloudEvents uses data. The event_id is stable across receivers and
    attempts, independently of each receiver delivery ID.

    """

    event_id: UUID
    job_id: UUID
    app_id: UUID
    mode: EventRecoveryFinishedWebhookPayloadMode
    state: EventRecoveryFinishedWebhookPayloadState
    outcome: EventRecoveryFinishedWebhookPayloadOutcome
    selected_count: int
    pending_count: EventRecoveryFinishedWebhookPayloadPendingCount
    queued_count: int
    skipped_count: int
    cancelled_count: int
    created_at: datetime.datetime
    expires_at: datetime.datetime
    completed_at: datetime.datetime

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
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        event_id = UUID(d.pop("event_id"))

        job_id = UUID(d.pop("job_id"))

        app_id = UUID(d.pop("app_id"))

        mode = check_event_recovery_finished_webhook_payload_mode(d.pop("mode"))

        state = check_event_recovery_finished_webhook_payload_state(d.pop("state"))

        outcome = check_event_recovery_finished_webhook_payload_outcome(d.pop("outcome"))

        selected_count = d.pop("selected_count")

        pending_count = check_event_recovery_finished_webhook_payload_pending_count(d.pop("pending_count"))

        queued_count = d.pop("queued_count")

        skipped_count = d.pop("skipped_count")

        cancelled_count = d.pop("cancelled_count")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        completed_at = datetime.datetime.fromisoformat(d.pop("completed_at"))

        event_recovery_finished_webhook_payload = cls(
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
        )

        return event_recovery_finished_webhook_payload
