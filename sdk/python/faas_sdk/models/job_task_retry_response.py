from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.job_run_response import JobRunResponse
    from ..models.job_task_response import JobTaskResponse


T = TypeVar("T", bound="JobTaskRetryResponse")


@_attrs_define
class JobTaskRetryResponse:
    """POST .../tasks/{idx}/retry response. The task is re-queued with capped backoff."""

    task: JobTaskResponse
    """Wire projection of state.JobTask. LeaseToken is intentionally omitted (internal dispatch primitive)."""
    run: JobRunResponse
    """Wire projection of state.JobRun. Aggregate counters are recomputed by schedd after every terminal task
    transition."""
    retried_at: datetime.datetime
    next_attempt_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        task = self.task.to_dict()

        run = self.run.to_dict()

        retried_at = self.retried_at.isoformat()

        next_attempt_at = self.next_attempt_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "task": task,
                "run": run,
                "retried_at": retried_at,
                "next_attempt_at": next_attempt_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.job_run_response import JobRunResponse
        from ..models.job_task_response import JobTaskResponse

        d = dict(src_dict)
        task = JobTaskResponse.from_dict(d.pop("task"))

        run = JobRunResponse.from_dict(d.pop("run"))

        retried_at = datetime.datetime.fromisoformat(d.pop("retried_at"))

        next_attempt_at = datetime.datetime.fromisoformat(d.pop("next_attempt_at"))

        job_task_retry_response = cls(
            task=task,
            run=run,
            retried_at=retried_at,
            next_attempt_at=next_attempt_at,
        )

        job_task_retry_response.additional_properties = d
        return job_task_retry_response

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
