from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.realtime_consumer_group_metrics_bucket_seconds import (
    RealtimeConsumerGroupMetricsBucketSeconds,
    check_realtime_consumer_group_metrics_bucket_seconds,
)

if TYPE_CHECKING:
    from ..models.realtime_consumer_group_metric_bucket import RealtimeConsumerGroupMetricBucket


T = TypeVar("T", bound="RealtimeConsumerGroupMetrics")


@_attrs_define
class RealtimeConsumerGroupMetrics:
    """Realtime Consumer Group Metrics."""

    bucket_seconds: RealtimeConsumerGroupMetricsBucketSeconds
    buckets: list[RealtimeConsumerGroupMetricBucket]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        bucket_seconds: int = self.bucket_seconds

        buckets = []
        for buckets_item_data in self.buckets:
            buckets_item = buckets_item_data.to_dict()
            buckets.append(buckets_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "bucket_seconds": bucket_seconds,
                "buckets": buckets,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.realtime_consumer_group_metric_bucket import RealtimeConsumerGroupMetricBucket

        d = dict(src_dict)
        bucket_seconds = check_realtime_consumer_group_metrics_bucket_seconds(d.pop("bucket_seconds"))

        buckets = []
        _buckets = d.pop("buckets")
        for buckets_item_data in _buckets:
            buckets_item = RealtimeConsumerGroupMetricBucket.from_dict(buckets_item_data)

            buckets.append(buckets_item)

        realtime_consumer_group_metrics = cls(
            bucket_seconds=bucket_seconds,
            buckets=buckets,
        )

        realtime_consumer_group_metrics.additional_properties = d
        return realtime_consumer_group_metrics

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
