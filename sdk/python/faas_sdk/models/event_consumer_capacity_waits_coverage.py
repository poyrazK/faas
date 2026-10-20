from typing import Literal

EventConsumerCapacityWaitsCoverage = Literal["all_pending_application_recipients"]

EVENT_CONSUMER_CAPACITY_WAITS_COVERAGE_VALUES: set[EventConsumerCapacityWaitsCoverage] = {
    "all_pending_application_recipients",
}


def check_event_consumer_capacity_waits_coverage(value: str) -> EventConsumerCapacityWaitsCoverage:
    if value in EVENT_CONSUMER_CAPACITY_WAITS_COVERAGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_CONSUMER_CAPACITY_WAITS_COVERAGE_VALUES!r}")
