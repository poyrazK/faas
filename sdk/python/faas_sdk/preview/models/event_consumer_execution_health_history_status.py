from typing import Literal

EventConsumerExecutionHealthHistoryStatus = Literal["complete", "partial", "unknown"]

EVENT_CONSUMER_EXECUTION_HEALTH_HISTORY_STATUS_VALUES: set[EventConsumerExecutionHealthHistoryStatus] = {
    "complete",
    "partial",
    "unknown",
}


def check_event_consumer_execution_health_history_status(value: str) -> EventConsumerExecutionHealthHistoryStatus:
    if value in EVENT_CONSUMER_EXECUTION_HEALTH_HISTORY_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_CONSUMER_EXECUTION_HEALTH_HISTORY_STATUS_VALUES!r}"
    )
