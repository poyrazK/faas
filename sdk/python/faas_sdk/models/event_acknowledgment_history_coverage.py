from typing import Literal

EventAcknowledgmentHistoryCoverage = Literal["retained_coalesced_deferred_outcomes"]

EVENT_ACKNOWLEDGMENT_HISTORY_COVERAGE_VALUES: set[EventAcknowledgmentHistoryCoverage] = {
    "retained_coalesced_deferred_outcomes",
}


def check_event_acknowledgment_history_coverage(value: str) -> EventAcknowledgmentHistoryCoverage:
    if value in EVENT_ACKNOWLEDGMENT_HISTORY_COVERAGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_ACKNOWLEDGMENT_HISTORY_COVERAGE_VALUES!r}")
