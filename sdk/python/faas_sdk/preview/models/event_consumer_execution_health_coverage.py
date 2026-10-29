from typing import Literal

EventConsumerExecutionHealthCoverage = Literal["bounded_retained_execution_roots"]

EVENT_CONSUMER_EXECUTION_HEALTH_COVERAGE_VALUES: set[EventConsumerExecutionHealthCoverage] = {
    "bounded_retained_execution_roots",
}


def check_event_consumer_execution_health_coverage(value: str) -> EventConsumerExecutionHealthCoverage:
    if value in EVENT_CONSUMER_EXECUTION_HEALTH_COVERAGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_CONSUMER_EXECUTION_HEALTH_COVERAGE_VALUES!r}")
