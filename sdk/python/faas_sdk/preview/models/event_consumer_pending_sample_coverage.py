from typing import Literal

EventConsumerPendingSampleCoverage = Literal["oldest_pending_recipients"]

EVENT_CONSUMER_PENDING_SAMPLE_COVERAGE_VALUES: set[EventConsumerPendingSampleCoverage] = {
    "oldest_pending_recipients",
}


def check_event_consumer_pending_sample_coverage(value: str) -> EventConsumerPendingSampleCoverage:
    if value in EVENT_CONSUMER_PENDING_SAMPLE_COVERAGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_CONSUMER_PENDING_SAMPLE_COVERAGE_VALUES!r}")
