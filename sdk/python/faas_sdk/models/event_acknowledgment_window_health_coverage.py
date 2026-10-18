from typing import Literal

EventAcknowledgmentWindowHealthCoverage = Literal["recorded_full_minute_buckets"]

EVENT_ACKNOWLEDGMENT_WINDOW_HEALTH_COVERAGE_VALUES: set[EventAcknowledgmentWindowHealthCoverage] = {
    "recorded_full_minute_buckets",
}


def check_event_acknowledgment_window_health_coverage(value: str) -> EventAcknowledgmentWindowHealthCoverage:
    if value in EVENT_ACKNOWLEDGMENT_WINDOW_HEALTH_COVERAGE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_ACKNOWLEDGMENT_WINDOW_HEALTH_COVERAGE_VALUES!r}"
    )
