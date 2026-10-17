from typing import Literal

EventConsumerProcessingHealthCoverage = Literal["retained_deferred_invocations"]

EVENT_CONSUMER_PROCESSING_HEALTH_COVERAGE_VALUES: set[EventConsumerProcessingHealthCoverage] = {
    "retained_deferred_invocations",
}


def check_event_consumer_processing_health_coverage(value: str) -> EventConsumerProcessingHealthCoverage:
    if value in EVENT_CONSUMER_PROCESSING_HEALTH_COVERAGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_CONSUMER_PROCESSING_HEALTH_COVERAGE_VALUES!r}")
