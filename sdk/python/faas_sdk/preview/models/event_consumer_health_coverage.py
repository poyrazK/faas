from typing import Literal

EventConsumerHealthCoverage = Literal["bounded_recorded_outcomes"]

EVENT_CONSUMER_HEALTH_COVERAGE_VALUES: set[EventConsumerHealthCoverage] = {
    "bounded_recorded_outcomes",
}


def check_event_consumer_health_coverage(value: str) -> EventConsumerHealthCoverage:
    if value in EVENT_CONSUMER_HEALTH_COVERAGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_CONSUMER_HEALTH_COVERAGE_VALUES!r}")
