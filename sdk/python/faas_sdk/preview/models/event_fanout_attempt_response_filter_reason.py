from typing import Literal

EventFanoutAttemptResponseFilterReason = Literal["schema_version_mismatch"]

EVENT_FANOUT_ATTEMPT_RESPONSE_FILTER_REASON_VALUES: set[EventFanoutAttemptResponseFilterReason] = {
    "schema_version_mismatch",
}


def check_event_fanout_attempt_response_filter_reason(value: str) -> EventFanoutAttemptResponseFilterReason:
    if value in EVENT_FANOUT_ATTEMPT_RESPONSE_FILTER_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_FANOUT_ATTEMPT_RESPONSE_FILTER_REASON_VALUES!r}"
    )
