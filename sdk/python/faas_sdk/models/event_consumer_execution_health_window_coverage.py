from typing import Literal

EventConsumerExecutionHealthWindowCoverage = Literal["durable_execution_outcomes"]

EVENT_CONSUMER_EXECUTION_HEALTH_WINDOW_COVERAGE_VALUES: set[EventConsumerExecutionHealthWindowCoverage] = {
    "durable_execution_outcomes",
}


def check_event_consumer_execution_health_window_coverage(value: str) -> EventConsumerExecutionHealthWindowCoverage:
    if value in EVENT_CONSUMER_EXECUTION_HEALTH_WINDOW_COVERAGE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_CONSUMER_EXECUTION_HEALTH_WINDOW_COVERAGE_VALUES!r}"
    )
