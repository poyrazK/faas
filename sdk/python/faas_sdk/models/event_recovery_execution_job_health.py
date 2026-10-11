from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.event_recovery_execution_job_health_state import (
    EventRecoveryExecutionJobHealthState,
    check_event_recovery_execution_job_health_state,
)
from ..models.event_recovery_execution_job_health_status import (
    EventRecoveryExecutionJobHealthStatus,
    check_event_recovery_execution_job_health_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_recovery_execution_summary import EventRecoveryExecutionSummary


T = TypeVar("T", bound="EventRecoveryExecutionJobHealth")


@_attrs_define
class EventRecoveryExecutionJobHealth:
    """Wait age starts at admission completion; prolonged waits do not assert scheduler or handler failure. retain_until is
    the nominal job retention boundary. Unknown includes admitted items without replay identity. Awaiting saved results
    counts known terminal observations without saved confirmation.

    """

    job_id: UUID
    state: EventRecoveryExecutionJobHealthState
    status: EventRecoveryExecutionJobHealthStatus
    completed_at: datetime.datetime
    wait_age_seconds: float
    retain_until: datetime.datetime
    prolonged_wait: bool
    retention_risk: bool
    notification_pending: bool
    queued_count: int
    untracked_count: int
    unknown_count: int
    unresolved_count: int
    awaiting_saved_results_count: int
    execution: EventRecoveryExecutionSummary
    """Observations of admitted execution-mode items, preferring saved terminal results over live records. Legacy
    admissions without evidence remain unknown. Counts sum to tracked_count and are separate from job admission
    state. Omitted for routing recovery. Saved results share recovery job retention."""
    parent_job_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        job_id = str(self.job_id)

        state: str = self.state

        status: str = self.status

        completed_at = self.completed_at.isoformat()

        wait_age_seconds = self.wait_age_seconds

        retain_until = self.retain_until.isoformat()

        prolonged_wait = self.prolonged_wait

        retention_risk = self.retention_risk

        notification_pending = self.notification_pending

        queued_count = self.queued_count

        untracked_count = self.untracked_count

        unknown_count = self.unknown_count

        unresolved_count = self.unresolved_count

        awaiting_saved_results_count = self.awaiting_saved_results_count

        execution = self.execution.to_dict()

        parent_job_id: str | Unset = UNSET
        if not isinstance(self.parent_job_id, Unset):
            parent_job_id = str(self.parent_job_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "job_id": job_id,
                "state": state,
                "status": status,
                "completed_at": completed_at,
                "wait_age_seconds": wait_age_seconds,
                "retain_until": retain_until,
                "prolonged_wait": prolonged_wait,
                "retention_risk": retention_risk,
                "notification_pending": notification_pending,
                "queued_count": queued_count,
                "untracked_count": untracked_count,
                "unknown_count": unknown_count,
                "unresolved_count": unresolved_count,
                "awaiting_saved_results_count": awaiting_saved_results_count,
                "execution": execution,
            }
        )
        if parent_job_id is not UNSET:
            field_dict["parent_job_id"] = parent_job_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_recovery_execution_summary import EventRecoveryExecutionSummary

        d = dict(src_dict)
        job_id = UUID(d.pop("job_id"))

        state = check_event_recovery_execution_job_health_state(d.pop("state"))

        status = check_event_recovery_execution_job_health_status(d.pop("status"))

        completed_at = datetime.datetime.fromisoformat(d.pop("completed_at"))

        wait_age_seconds = d.pop("wait_age_seconds")

        retain_until = datetime.datetime.fromisoformat(d.pop("retain_until"))

        prolonged_wait = d.pop("prolonged_wait")

        retention_risk = d.pop("retention_risk")

        notification_pending = d.pop("notification_pending")

        queued_count = d.pop("queued_count")

        untracked_count = d.pop("untracked_count")

        unknown_count = d.pop("unknown_count")

        unresolved_count = d.pop("unresolved_count")

        awaiting_saved_results_count = d.pop("awaiting_saved_results_count")

        execution = EventRecoveryExecutionSummary.from_dict(d.pop("execution"))

        _parent_job_id = d.pop("parent_job_id", UNSET)
        parent_job_id: UUID | Unset
        if isinstance(_parent_job_id, Unset):
            parent_job_id = UNSET
        else:
            parent_job_id = UUID(_parent_job_id)

        event_recovery_execution_job_health = cls(
            job_id=job_id,
            state=state,
            status=status,
            completed_at=completed_at,
            wait_age_seconds=wait_age_seconds,
            retain_until=retain_until,
            prolonged_wait=prolonged_wait,
            retention_risk=retention_risk,
            notification_pending=notification_pending,
            queued_count=queued_count,
            untracked_count=untracked_count,
            unknown_count=unknown_count,
            unresolved_count=unresolved_count,
            awaiting_saved_results_count=awaiting_saved_results_count,
            execution=execution,
            parent_job_id=parent_job_id,
        )

        event_recovery_execution_job_health.additional_properties = d
        return event_recovery_execution_job_health

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
