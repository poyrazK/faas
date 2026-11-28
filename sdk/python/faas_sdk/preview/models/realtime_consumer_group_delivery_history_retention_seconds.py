from typing import Literal

RealtimeConsumerGroupDeliveryHistoryRetentionSeconds = Literal[86400]

REALTIME_CONSUMER_GROUP_DELIVERY_HISTORY_RETENTION_SECONDS_VALUES: set[
    RealtimeConsumerGroupDeliveryHistoryRetentionSeconds
] = {
    86400,
}


def check_realtime_consumer_group_delivery_history_retention_seconds(
    value: int,
) -> RealtimeConsumerGroupDeliveryHistoryRetentionSeconds:
    if value in REALTIME_CONSUMER_GROUP_DELIVERY_HISTORY_RETENTION_SECONDS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REALTIME_CONSUMER_GROUP_DELIVERY_HISTORY_RETENTION_SECONDS_VALUES!r}"
    )
