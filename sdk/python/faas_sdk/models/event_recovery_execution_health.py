from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.event_recovery_execution_health_coverage import (
    EventRecoveryExecutionHealthCoverage,
    check_event_recovery_execution_health_coverage,
)
from ..models.event_recovery_execution_health_job_limit import (
    EventRecoveryExecutionHealthJobLimit,
    check_event_recovery_execution_health_job_limit,
)

if TYPE_CHECKING:
    from ..models.event_recovery_execution_job_health import EventRecoveryExecutionJobHealth


T = TypeVar("T", bound="EventRecoveryExecutionHealth")


@_attrs_define
class EventRecoveryExecutionHealth:
    """Oldest retained terminal admission jobs still missing exact saved execution results. Counts overlap and are lower
    bounds when counts_complete is false. Reads do not capture results or notifications.

    """

    coverage: EventRecoveryExecutionHealthCoverage
    observed_jobs: int
    waiting_jobs: int
    prolonged_wait_jobs: int
    unknown_jobs: int
    retention_risk_jobs: int
    counts_complete: bool
    job_limit: EventRecoveryExecutionHealthJobLimit
    wait_warning_seconds: int
    retention_warning_seconds: int
    jobs: list[EventRecoveryExecutionJobHealth]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        coverage: str = self.coverage

        observed_jobs = self.observed_jobs

        waiting_jobs = self.waiting_jobs

        prolonged_wait_jobs = self.prolonged_wait_jobs

        unknown_jobs = self.unknown_jobs

        retention_risk_jobs = self.retention_risk_jobs

        counts_complete = self.counts_complete

        job_limit: int = self.job_limit

        wait_warning_seconds = self.wait_warning_seconds

        retention_warning_seconds = self.retention_warning_seconds

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
                "waiting_jobs": waiting_jobs,
                "prolonged_wait_jobs": prolonged_wait_jobs,
                "unknown_jobs": unknown_jobs,
                "retention_risk_jobs": retention_risk_jobs,
                "counts_complete": counts_complete,
                "job_limit": job_limit,
                "wait_warning_seconds": wait_warning_seconds,
                "retention_warning_seconds": retention_warning_seconds,
                "jobs": jobs,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_recovery_execution_job_health import EventRecoveryExecutionJobHealth

        d = dict(src_dict)
        coverage = check_event_recovery_execution_health_coverage(d.pop("coverage"))

        observed_jobs = d.pop("observed_jobs")

        waiting_jobs = d.pop("waiting_jobs")

        prolonged_wait_jobs = d.pop("prolonged_wait_jobs")

        unknown_jobs = d.pop("unknown_jobs")

        retention_risk_jobs = d.pop("retention_risk_jobs")

        counts_complete = d.pop("counts_complete")

        job_limit = check_event_recovery_execution_health_job_limit(d.pop("job_limit"))

        wait_warning_seconds = d.pop("wait_warning_seconds")

        retention_warning_seconds = d.pop("retention_warning_seconds")

        jobs = []
        _jobs = d.pop("jobs")
        for jobs_item_data in _jobs:
            jobs_item = EventRecoveryExecutionJobHealth.from_dict(jobs_item_data)

            jobs.append(jobs_item)

        event_recovery_execution_health = cls(
            coverage=coverage,
            observed_jobs=observed_jobs,
            waiting_jobs=waiting_jobs,
            prolonged_wait_jobs=prolonged_wait_jobs,
            unknown_jobs=unknown_jobs,
            retention_risk_jobs=retention_risk_jobs,
            counts_complete=counts_complete,
            job_limit=job_limit,
            wait_warning_seconds=wait_warning_seconds,
            retention_warning_seconds=retention_warning_seconds,
            jobs=jobs,
        )

        event_recovery_execution_health.additional_properties = d
        return event_recovery_execution_health

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
