from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_execution_lineage_retry_job_summary import EventExecutionLineageRetryJobSummary


T = TypeVar("T", bound="EventExecutionLineageRetryJobs")


@_attrs_define
class EventExecutionLineageRetryJobs:
    jobs: list[EventExecutionLineageRetryJobSummary]
    next_after: str | Unset = UNSET
    """Continuation cursor; omitted when no further page was observed."""

    def to_dict(self) -> dict[str, Any]:
        jobs = []
        for jobs_item_data in self.jobs:
            jobs_item = jobs_item_data.to_dict()
            jobs.append(jobs_item)

        next_after = self.next_after

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "jobs": jobs,
            }
        )
        if next_after is not UNSET:
            field_dict["next_after"] = next_after

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_execution_lineage_retry_job_summary import EventExecutionLineageRetryJobSummary

        d = dict(src_dict)
        jobs = []
        _jobs = d.pop("jobs")
        for jobs_item_data in _jobs:
            jobs_item = EventExecutionLineageRetryJobSummary.from_dict(jobs_item_data)

            jobs.append(jobs_item)

        next_after = d.pop("next_after", UNSET)

        event_execution_lineage_retry_jobs = cls(
            jobs=jobs,
            next_after=next_after,
        )

        return event_execution_lineage_retry_jobs
