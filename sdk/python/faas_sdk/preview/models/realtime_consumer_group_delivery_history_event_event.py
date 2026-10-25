from typing import Literal

RealtimeConsumerGroupDeliveryHistoryEventEvent = Literal[
    "claimed",
    "completed",
    "dead_lettered",
    "deadline_expired",
    "invalid_deadline",
    "lease_expired",
    "released",
    "replayed",
    "retried",
]

REALTIME_CONSUMER_GROUP_DELIVERY_HISTORY_EVENT_EVENT_VALUES: set[RealtimeConsumerGroupDeliveryHistoryEventEvent] = {
    "claimed",
    "completed",
    "dead_lettered",
    "deadline_expired",
    "invalid_deadline",
    "lease_expired",
    "released",
    "replayed",
    "retried",
}


def check_realtime_consumer_group_delivery_history_event_event(
    value: str,
) -> RealtimeConsumerGroupDeliveryHistoryEventEvent:
    if value in REALTIME_CONSUMER_GROUP_DELIVERY_HISTORY_EVENT_EVENT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REALTIME_CONSUMER_GROUP_DELIVERY_HISTORY_EVENT_EVENT_VALUES!r}"
    )
