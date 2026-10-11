from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_recovery_execution_health import EventRecoveryExecutionHealth
    from ..models.event_recovery_job_health import EventRecoveryJobHealth
    from ..models.event_recovery_notifications_health import EventRecoveryNotificationsHealth


T = TypeVar("T", bound="EventRecoveryHealth")


@_attrs_define
class EventRecoveryHealth:
    """Application admission health and bounded unresolved execution health with sampled actionable jobs."""

    capacity_wait_warning_seconds: int
    capacity_waiting_jobs: int
    """Running capacity-wait jobs observed within the five-minute freshness grace."""
    prolonged_capacity_wait_jobs: int
    """Fresh running capacity episodes lasting at least fifteen minutes; excludes paused and expired jobs and
    scheduler stalls."""
    app_id: UUID
    observed_at: datetime.datetime
    stall_grace_seconds: int
    expiry_warning_seconds: int
    running_jobs: int
    paused_jobs: int
    stalled_jobs: int
    expiring_jobs: int
    """Running jobs with pending work approaching expiry; excludes paused jobs."""
    paused_expiring_jobs: int
    jobs: list[EventRecoveryJobHealth]
    notifications: EventRecoveryNotificationsHealth | Unset = UNSET
    """Bounded retained notification candidates, oldest admission completion first. Candidate-set completeness is
    separate from phase evidence completeness. Counts describe jobs and can overlap; reads do not capture or retry
    notifications."""
    execution: EventRecoveryExecutionHealth | Unset = UNSET
    """Oldest retained terminal admission jobs still missing exact saved execution results. Counts overlap and are
    lower bounds when counts_complete is false. Reads do not capture results or notifications."""

    def to_dict(self) -> dict[str, Any]:
        capacity_wait_warning_seconds = self.capacity_wait_warning_seconds

        capacity_waiting_jobs = self.capacity_waiting_jobs

        prolonged_capacity_wait_jobs = self.prolonged_capacity_wait_jobs

        app_id = str(self.app_id)

        observed_at = self.observed_at.isoformat()

        stall_grace_seconds = self.stall_grace_seconds

        expiry_warning_seconds = self.expiry_warning_seconds

        running_jobs = self.running_jobs

        paused_jobs = self.paused_jobs

        stalled_jobs = self.stalled_jobs

        expiring_jobs = self.expiring_jobs

        paused_expiring_jobs = self.paused_expiring_jobs

        jobs = []
        for jobs_item_data in self.jobs:
            jobs_item = jobs_item_data.to_dict()
            jobs.append(jobs_item)

        notifications: dict[str, Any] | Unset = UNSET
        if not isinstance(self.notifications, Unset):
            notifications = self.notifications.to_dict()

        execution: dict[str, Any] | Unset = UNSET
        if not isinstance(self.execution, Unset):
            execution = self.execution.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "capacity_wait_warning_seconds": capacity_wait_warning_seconds,
                "capacity_waiting_jobs": capacity_waiting_jobs,
                "prolonged_capacity_wait_jobs": prolonged_capacity_wait_jobs,
                "app_id": app_id,
                "observed_at": observed_at,
                "stall_grace_seconds": stall_grace_seconds,
                "expiry_warning_seconds": expiry_warning_seconds,
                "running_jobs": running_jobs,
                "paused_jobs": paused_jobs,
                "stalled_jobs": stalled_jobs,
                "expiring_jobs": expiring_jobs,
                "paused_expiring_jobs": paused_expiring_jobs,
                "jobs": jobs,
            }
        )
        if notifications is not UNSET:
            field_dict["notifications"] = notifications
        if execution is not UNSET:
            field_dict["execution"] = execution

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_recovery_execution_health import EventRecoveryExecutionHealth
        from ..models.event_recovery_job_health import EventRecoveryJobHealth
        from ..models.event_recovery_notifications_health import EventRecoveryNotificationsHealth

        d = dict(src_dict)
        capacity_wait_warning_seconds = d.pop("capacity_wait_warning_seconds")

        capacity_waiting_jobs = d.pop("capacity_waiting_jobs")

        prolonged_capacity_wait_jobs = d.pop("prolonged_capacity_wait_jobs")

        app_id = UUID(d.pop("app_id"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        stall_grace_seconds = d.pop("stall_grace_seconds")

        expiry_warning_seconds = d.pop("expiry_warning_seconds")

        running_jobs = d.pop("running_jobs")

        paused_jobs = d.pop("paused_jobs")

        stalled_jobs = d.pop("stalled_jobs")

        expiring_jobs = d.pop("expiring_jobs")

        paused_expiring_jobs = d.pop("paused_expiring_jobs")

        jobs = []
        _jobs = d.pop("jobs")
        for jobs_item_data in _jobs:
            jobs_item = EventRecoveryJobHealth.from_dict(jobs_item_data)

            jobs.append(jobs_item)

        _notifications = d.pop("notifications", UNSET)
        notifications: EventRecoveryNotificationsHealth | Unset
        if isinstance(_notifications, Unset):
            notifications = UNSET
        else:
            notifications = EventRecoveryNotificationsHealth.from_dict(_notifications)

        _execution = d.pop("execution", UNSET)
        execution: EventRecoveryExecutionHealth | Unset
        if isinstance(_execution, Unset):
            execution = UNSET
        else:
            execution = EventRecoveryExecutionHealth.from_dict(_execution)

        event_recovery_health = cls(
            capacity_wait_warning_seconds=capacity_wait_warning_seconds,
            capacity_waiting_jobs=capacity_waiting_jobs,
            prolonged_capacity_wait_jobs=prolonged_capacity_wait_jobs,
            app_id=app_id,
            observed_at=observed_at,
            stall_grace_seconds=stall_grace_seconds,
            expiry_warning_seconds=expiry_warning_seconds,
            running_jobs=running_jobs,
            paused_jobs=paused_jobs,
            stalled_jobs=stalled_jobs,
            expiring_jobs=expiring_jobs,
            paused_expiring_jobs=paused_expiring_jobs,
            jobs=jobs,
            notifications=notifications,
            execution=execution,
        )

        return event_recovery_health
