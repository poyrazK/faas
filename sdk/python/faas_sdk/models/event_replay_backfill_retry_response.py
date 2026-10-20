from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.event_replay_backfill_job_response import EventReplayBackfillJobResponse


T = TypeVar("T", bound="EventReplayBackfillRetryResponse")


@_attrs_define
class EventReplayBackfillRetryResponse:
    """Number of failed deliveries retried and the resulting job status."""

    retried_count: int
    remaining_retryable_count: int
    """Failed routing items that can still be requeued from this job."""
    job: EventReplayBackfillJobResponse
    """Progress and immutable selection details for one durable event backfill."""

    def to_dict(self) -> dict[str, Any]:
        retried_count = self.retried_count

        remaining_retryable_count = self.remaining_retryable_count

        job = self.job.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "retried_count": retried_count,
                "remaining_retryable_count": remaining_retryable_count,
                "job": job,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_replay_backfill_job_response import EventReplayBackfillJobResponse

        d = dict(src_dict)
        retried_count = d.pop("retried_count")

        remaining_retryable_count = d.pop("remaining_retryable_count")

        job = EventReplayBackfillJobResponse.from_dict(d.pop("job"))

        event_replay_backfill_retry_response = cls(
            retried_count=retried_count,
            remaining_retryable_count=remaining_retryable_count,
            job=job,
        )

        return event_replay_backfill_retry_response
