from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_recovery_job_coverage import EventRecoveryJobCoverage, check_event_recovery_job_coverage
from ..models.event_recovery_job_state import EventRecoveryJobState, check_event_recovery_job_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_recovery_execution_summary import EventRecoveryExecutionSummary
    from ..models.event_recovery_request import EventRecoveryRequest


T = TypeVar("T", bound="EventRecoveryJob")


@_attrs_define
class EventRecoveryJob:
    """Durable recovery job identity, frozen selection, pacing state and admission progress counters."""

    rate_per_second: int
    """Current admission rate; selection.rate_per_second retains the original requested rate."""
    id: UUID
    app_id: UUID
    coverage: EventRecoveryJobCoverage
    selection: EventRecoveryRequest
    """Select routing failures (default) or the latest replayable retained execution per application event
    consumer. Execution mode includes publication and materialized backfill recipients, excludes workflows and
    object notifications, and requires retained admission and execution records. Creation freezes its own selection;
    preview is advisory."""
    state: EventRecoveryJobState
    selected_count: int
    pending_count: int
    queued_count: int
    skipped_count: int
    cancelled_count: int
    created_at: datetime.datetime
    updated_at: datetime.datetime
    expires_at: datetime.datetime
    paused_at: datetime.datetime | Unset = UNSET
    """Start time of the current pause; present only while paused."""
    execution: EventRecoveryExecutionSummary | Unset = UNSET
    """Observations of admitted execution-mode items, preferring saved terminal results over live records. Legacy
    admissions without evidence remain unknown. Counts sum to tracked_count and are separate from job admission
    state. Omitted for routing recovery. Saved results share recovery job retention."""
    completed_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        rate_per_second = self.rate_per_second

        id = str(self.id)

        app_id = str(self.app_id)

        coverage: str = self.coverage

        selection = self.selection.to_dict()

        state: str = self.state

        selected_count = self.selected_count

        pending_count = self.pending_count

        queued_count = self.queued_count

        skipped_count = self.skipped_count

        cancelled_count = self.cancelled_count

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        expires_at = self.expires_at.isoformat()

        paused_at: str | Unset = UNSET
        if not isinstance(self.paused_at, Unset):
            paused_at = self.paused_at.isoformat()

        execution: dict[str, Any] | Unset = UNSET
        if not isinstance(self.execution, Unset):
            execution = self.execution.to_dict()

        completed_at: str | Unset = UNSET
        if not isinstance(self.completed_at, Unset):
            completed_at = self.completed_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "rate_per_second": rate_per_second,
                "id": id,
                "app_id": app_id,
                "coverage": coverage,
                "selection": selection,
                "state": state,
                "selected_count": selected_count,
                "pending_count": pending_count,
                "queued_count": queued_count,
                "skipped_count": skipped_count,
                "cancelled_count": cancelled_count,
                "created_at": created_at,
                "updated_at": updated_at,
                "expires_at": expires_at,
            }
        )
        if paused_at is not UNSET:
            field_dict["paused_at"] = paused_at
        if execution is not UNSET:
            field_dict["execution"] = execution
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_recovery_execution_summary import EventRecoveryExecutionSummary
        from ..models.event_recovery_request import EventRecoveryRequest

        d = dict(src_dict)
        rate_per_second = d.pop("rate_per_second")

        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        coverage = check_event_recovery_job_coverage(d.pop("coverage"))

        selection = EventRecoveryRequest.from_dict(d.pop("selection"))

        state = check_event_recovery_job_state(d.pop("state"))

        selected_count = d.pop("selected_count")

        pending_count = d.pop("pending_count")

        queued_count = d.pop("queued_count")

        skipped_count = d.pop("skipped_count")

        cancelled_count = d.pop("cancelled_count")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        _paused_at = d.pop("paused_at", UNSET)
        paused_at: datetime.datetime | Unset
        if isinstance(_paused_at, Unset):
            paused_at = UNSET
        else:
            paused_at = datetime.datetime.fromisoformat(_paused_at)

        _execution = d.pop("execution", UNSET)
        execution: EventRecoveryExecutionSummary | Unset
        if isinstance(_execution, Unset):
            execution = UNSET
        else:
            execution = EventRecoveryExecutionSummary.from_dict(_execution)

        _completed_at = d.pop("completed_at", UNSET)
        completed_at: datetime.datetime | Unset
        if isinstance(_completed_at, Unset):
            completed_at = UNSET
        else:
            completed_at = datetime.datetime.fromisoformat(_completed_at)

        event_recovery_job = cls(
            rate_per_second=rate_per_second,
            id=id,
            app_id=app_id,
            coverage=coverage,
            selection=selection,
            state=state,
            selected_count=selected_count,
            pending_count=pending_count,
            queued_count=queued_count,
            skipped_count=skipped_count,
            cancelled_count=cancelled_count,
            created_at=created_at,
            updated_at=updated_at,
            expires_at=expires_at,
            paused_at=paused_at,
            execution=execution,
            completed_at=completed_at,
        )

        return event_recovery_job
