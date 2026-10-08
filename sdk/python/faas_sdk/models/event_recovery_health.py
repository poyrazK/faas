from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.event_recovery_job_health import EventRecoveryJobHealth


T = TypeVar("T", bound="EventRecoveryHealth")


@_attrs_define
class EventRecoveryHealth:
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

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_recovery_job_health import EventRecoveryJobHealth

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
        )

        return event_recovery_health
