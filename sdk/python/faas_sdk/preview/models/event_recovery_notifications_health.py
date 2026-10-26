from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.event_recovery_notifications_health_coverage import (
    EventRecoveryNotificationsHealthCoverage,
    check_event_recovery_notifications_health_coverage,
)
from ..models.event_recovery_notifications_health_job_limit import (
    EventRecoveryNotificationsHealthJobLimit,
    check_event_recovery_notifications_health_job_limit,
)

if TYPE_CHECKING:
    from ..models.event_recovery_notification_health_counts import EventRecoveryNotificationHealthCounts
    from ..models.event_recovery_notification_job_health import EventRecoveryNotificationJobHealth


T = TypeVar("T", bound="EventRecoveryNotificationsHealth")


@_attrs_define
class EventRecoveryNotificationsHealth:
    """Bounded retained notification candidates, oldest admission completion first. Candidate-set completeness is separate
    from phase evidence completeness. Counts describe jobs and can overlap; reads do not capture or retry notifications.

    """

    coverage: EventRecoveryNotificationsHealthCoverage
    observed_jobs: int
    counts_complete: bool
    job_limit: EventRecoveryNotificationsHealthJobLimit
    overdue_grace_seconds: int
    admission: EventRecoveryNotificationHealthCounts
    """Per-phase notification job counts. Incomplete evidence or candidate truncation makes these lower bounds;
    partial observations cannot clear alerts and can only trigger satisfied greater-than comparisons."""
    execution: EventRecoveryNotificationHealthCounts
    """Per-phase notification job counts. Incomplete evidence or candidate truncation makes these lower bounds;
    partial observations cannot clear alerts and can only trigger satisfied greater-than comparisons."""
    jobs: list[EventRecoveryNotificationJobHealth]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        coverage: str = self.coverage

        observed_jobs = self.observed_jobs

        counts_complete = self.counts_complete

        job_limit: int = self.job_limit

        overdue_grace_seconds = self.overdue_grace_seconds

        admission = self.admission.to_dict()

        execution = self.execution.to_dict()

        jobs = []
        for jobs_item_data in self.jobs:
            jobs_item = jobs_item_data.to_dict()
            jobs.append(jobs_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "coverage": coverage,
                "observed_jobs": observed_jobs,
                "counts_complete": counts_complete,
                "job_limit": job_limit,
                "overdue_grace_seconds": overdue_grace_seconds,
                "admission": admission,
                "execution": execution,
                "jobs": jobs,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_recovery_notification_health_counts import EventRecoveryNotificationHealthCounts
        from ..models.event_recovery_notification_job_health import EventRecoveryNotificationJobHealth

        d = dict(src_dict)
        coverage = check_event_recovery_notifications_health_coverage(d.pop("coverage"))

        observed_jobs = d.pop("observed_jobs")

        counts_complete = d.pop("counts_complete")

        job_limit = check_event_recovery_notifications_health_job_limit(d.pop("job_limit"))

        overdue_grace_seconds = d.pop("overdue_grace_seconds")

        admission = EventRecoveryNotificationHealthCounts.from_dict(d.pop("admission"))

        execution = EventRecoveryNotificationHealthCounts.from_dict(d.pop("execution"))

        jobs = []
        _jobs = d.pop("jobs")
        for jobs_item_data in _jobs:
            jobs_item = EventRecoveryNotificationJobHealth.from_dict(jobs_item_data)

            jobs.append(jobs_item)

        event_recovery_notifications_health = cls(
            coverage=coverage,
            observed_jobs=observed_jobs,
            counts_complete=counts_complete,
            job_limit=job_limit,
            overdue_grace_seconds=overdue_grace_seconds,
            admission=admission,
            execution=execution,
            jobs=jobs,
        )

        event_recovery_notifications_health.additional_properties = d
        return event_recovery_notifications_health

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
