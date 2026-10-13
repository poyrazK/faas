from typing import Literal

EventConsumerExecutionHealthWindowHistoryStatus = Literal["complete", "partial", "unknown"]

EVENT_CONSUMER_EXECUTION_HEALTH_WINDOW_HISTORY_STATUS_VALUES: set[EventConsumerExecutionHealthWindowHistoryStatus] = {
    "complete",
    "partial",
    "unknown",
}


def check_event_consumer_execution_health_window_history_status(
    value: str,
) -> EventConsumerExecutionHealthWindowHistoryStatus:
    if value in EVENT_CONSUMER_EXECUTION_HEALTH_WINDOW_HISTORY_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_CONSUMER_EXECUTION_HEALTH_WINDOW_HISTORY_STATUS_VALUES!r}"
    )
