from typing import Literal

RealtimeConsumerGroupMetricsBucketSeconds = Literal[60]

REALTIME_CONSUMER_GROUP_METRICS_BUCKET_SECONDS_VALUES: set[RealtimeConsumerGroupMetricsBucketSeconds] = {
    60,
}


def check_realtime_consumer_group_metrics_bucket_seconds(value: int) -> RealtimeConsumerGroupMetricsBucketSeconds:
    if value in REALTIME_CONSUMER_GROUP_METRICS_BUCKET_SECONDS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REALTIME_CONSUMER_GROUP_METRICS_BUCKET_SECONDS_VALUES!r}"
    )
