from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.event_execution_lineage_retry_job_health_coverage import (
    EventExecutionLineageRetryJobHealthCoverage,
    check_event_execution_lineage_retry_job_health_coverage,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_execution_lineage_retry_job_health_sample import EventExecutionLineageRetryJobHealthSample


T = TypeVar("T", bound="EventExecutionLineageRetryJobHealth")


@_attrs_define
class EventExecutionLineageRetryJobHealth:
    """Bounded retained pending and paused lineage retry jobs at observed_at. Independent of the execution window and
    handler outcome completeness. Earliest retention deadlines are observed first. When counts_complete is false counts
    are lower bounds and oldest/next timestamps describe only observed jobs. Stalled pending is an advisory inactivity
    warning; pause/resume activity can reset inactivity without changing last progress. Legacy evaluated jobs without a
    progress timestamp report unknown progress. No handler payloads or case selections are included.

    """

    coverage: EventExecutionLineageRetryJobHealthCoverage
    counts_complete: bool
    job_limit: int
    inactivity_warning_seconds: int
    retention_warning_seconds: int
    observed_jobs: int
    pending_jobs: int
    paused_jobs: int
    remaining_cases: int
    pending_remaining_cases: int
    paused_remaining_cases: int
    stalled_pending_jobs: int
    retention_risk_jobs: int
    unknown_progress_jobs: int
    jobs: list[EventExecutionLineageRetryJobHealthSample]
    oldest_pending_activity_at: datetime.datetime | Unset = UNSET
    next_retain_until: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        coverage: str = self.coverage

        counts_complete = self.counts_complete

        job_limit = self.job_limit

        inactivity_warning_seconds = self.inactivity_warning_seconds

        retention_warning_seconds = self.retention_warning_seconds

        observed_jobs = self.observed_jobs

        pending_jobs = self.pending_jobs

        paused_jobs = self.paused_jobs

        remaining_cases = self.remaining_cases

        pending_remaining_cases = self.pending_remaining_cases

        paused_remaining_cases = self.paused_remaining_cases

        stalled_pending_jobs = self.stalled_pending_jobs

        retention_risk_jobs = self.retention_risk_jobs

        unknown_progress_jobs = self.unknown_progress_jobs

        jobs = []
        for jobs_item_data in self.jobs:
            jobs_item = jobs_item_data.to_dict()
            jobs.append(jobs_item)

        oldest_pending_activity_at: str | Unset = UNSET
        if not isinstance(self.oldest_pending_activity_at, Unset):
            oldest_pending_activity_at = self.oldest_pending_activity_at.isoformat()

        next_retain_until: str | Unset = UNSET
        if not isinstance(self.next_retain_until, Unset):
            next_retain_until = self.next_retain_until.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "coverage": coverage,
                "counts_complete": counts_complete,
                "job_limit": job_limit,
                "inactivity_warning_seconds": inactivity_warning_seconds,
                "retention_warning_seconds": retention_warning_seconds,
                "observed_jobs": observed_jobs,
                "pending_jobs": pending_jobs,
                "paused_jobs": paused_jobs,
                "remaining_cases": remaining_cases,
                "pending_remaining_cases": pending_remaining_cases,
                "paused_remaining_cases": paused_remaining_cases,
                "stalled_pending_jobs": stalled_pending_jobs,
                "retention_risk_jobs": retention_risk_jobs,
                "unknown_progress_jobs": unknown_progress_jobs,
                "jobs": jobs,
            }
        )
        if oldest_pending_activity_at is not UNSET:
            field_dict["oldest_pending_activity_at"] = oldest_pending_activity_at
        if next_retain_until is not UNSET:
            field_dict["next_retain_until"] = next_retain_until

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_execution_lineage_retry_job_health_sample import EventExecutionLineageRetryJobHealthSample

        d = dict(src_dict)
        coverage = check_event_execution_lineage_retry_job_health_coverage(d.pop("coverage"))

        counts_complete = d.pop("counts_complete")

        job_limit = d.pop("job_limit")

        inactivity_warning_seconds = d.pop("inactivity_warning_seconds")

        retention_warning_seconds = d.pop("retention_warning_seconds")

        observed_jobs = d.pop("observed_jobs")

        pending_jobs = d.pop("pending_jobs")

        paused_jobs = d.pop("paused_jobs")

        remaining_cases = d.pop("remaining_cases")

        pending_remaining_cases = d.pop("pending_remaining_cases")

        paused_remaining_cases = d.pop("paused_remaining_cases")

        stalled_pending_jobs = d.pop("stalled_pending_jobs")

        retention_risk_jobs = d.pop("retention_risk_jobs")

        unknown_progress_jobs = d.pop("unknown_progress_jobs")

        jobs = []
        _jobs = d.pop("jobs")
        for jobs_item_data in _jobs:
            jobs_item = EventExecutionLineageRetryJobHealthSample.from_dict(jobs_item_data)

            jobs.append(jobs_item)

        _oldest_pending_activity_at = d.pop("oldest_pending_activity_at", UNSET)
        oldest_pending_activity_at: datetime.datetime | Unset
        if isinstance(_oldest_pending_activity_at, Unset):
            oldest_pending_activity_at = UNSET
        else:
            oldest_pending_activity_at = datetime.datetime.fromisoformat(_oldest_pending_activity_at)

        _next_retain_until = d.pop("next_retain_until", UNSET)
        next_retain_until: datetime.datetime | Unset
        if isinstance(_next_retain_until, Unset):
            next_retain_until = UNSET
        else:
            next_retain_until = datetime.datetime.fromisoformat(_next_retain_until)

        event_execution_lineage_retry_job_health = cls(
            coverage=coverage,
            counts_complete=counts_complete,
            job_limit=job_limit,
            inactivity_warning_seconds=inactivity_warning_seconds,
            retention_warning_seconds=retention_warning_seconds,
            observed_jobs=observed_jobs,
            pending_jobs=pending_jobs,
            paused_jobs=paused_jobs,
            remaining_cases=remaining_cases,
            pending_remaining_cases=pending_remaining_cases,
            paused_remaining_cases=paused_remaining_cases,
            stalled_pending_jobs=stalled_pending_jobs,
            retention_risk_jobs=retention_risk_jobs,
            unknown_progress_jobs=unknown_progress_jobs,
            jobs=jobs,
            oldest_pending_activity_at=oldest_pending_activity_at,
            next_retain_until=next_retain_until,
        )

        return event_execution_lineage_retry_job_health
