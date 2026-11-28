from typing import Literal

EventConsumerWaitingReasonsCoverage = Literal["all_pending_application_recipients"]

EVENT_CONSUMER_WAITING_REASONS_COVERAGE_VALUES: set[EventConsumerWaitingReasonsCoverage] = {
    "all_pending_application_recipients",
}


def check_event_consumer_waiting_reasons_coverage(value: str) -> EventConsumerWaitingReasonsCoverage:
    if value in EVENT_CONSUMER_WAITING_REASONS_COVERAGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_CONSUMER_WAITING_REASONS_COVERAGE_VALUES!r}")
