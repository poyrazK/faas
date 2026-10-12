from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RealtimeConsumerGroupMetricBucket")


@_attrs_define
class RealtimeConsumerGroupMetricBucket:
    """Realtime Consumer Group Metric Bucket."""

    bucket_at: datetime.datetime
    first_claims: str
    completed: str
    """Successful attempts with an observed processing start; legacy in-flight attempts are excluded."""
    retries: str
    dead_letters: str
    queue_wait_milliseconds: float
    processing_milliseconds: float
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        bucket_at = self.bucket_at.isoformat()

        first_claims = self.first_claims

        completed = self.completed

        retries = self.retries

        dead_letters = self.dead_letters

        queue_wait_milliseconds = self.queue_wait_milliseconds

        processing_milliseconds = self.processing_milliseconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "bucket_at": bucket_at,
                "first_claims": first_claims,
                "completed": completed,
                "retries": retries,
                "dead_letters": dead_letters,
                "queue_wait_milliseconds": queue_wait_milliseconds,
                "processing_milliseconds": processing_milliseconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        bucket_at = datetime.datetime.fromisoformat(d.pop("bucket_at"))

        first_claims = d.pop("first_claims")

        completed = d.pop("completed")

        retries = d.pop("retries")

        dead_letters = d.pop("dead_letters")

        queue_wait_milliseconds = d.pop("queue_wait_milliseconds")

        processing_milliseconds = d.pop("processing_milliseconds")

        realtime_consumer_group_metric_bucket = cls(
            bucket_at=bucket_at,
            first_claims=first_claims,
            completed=completed,
            retries=retries,
            dead_letters=dead_letters,
            queue_wait_milliseconds=queue_wait_milliseconds,
            processing_milliseconds=processing_milliseconds,
        )

        realtime_consumer_group_metric_bucket.additional_properties = d
        return realtime_consumer_group_metric_bucket

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
