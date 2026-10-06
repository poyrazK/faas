from typing import Literal

EventFanoutAttemptHistoryResponseCoverage = Literal["bounded_recorded_outcomes"]

EVENT_FANOUT_ATTEMPT_HISTORY_RESPONSE_COVERAGE_VALUES: set[EventFanoutAttemptHistoryResponseCoverage] = {
    "bounded_recorded_outcomes",
}


def check_event_fanout_attempt_history_response_coverage(value: str) -> EventFanoutAttemptHistoryResponseCoverage:
    if value in EVENT_FANOUT_ATTEMPT_HISTORY_RESPONSE_COVERAGE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_FANOUT_ATTEMPT_HISTORY_RESPONSE_COVERAGE_VALUES!r}"
    )
