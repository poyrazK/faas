from typing import Literal

EventFanoutAttemptResponseFailureCode = Literal[
    "internal_error",
    "invalid_subscription",
    "invocation_enqueue_failed",
    "target_lookup_failed",
    "target_unavailable",
    "unknown",
]

EVENT_FANOUT_ATTEMPT_RESPONSE_FAILURE_CODE_VALUES: set[EventFanoutAttemptResponseFailureCode] = {
    "internal_error",
    "invalid_subscription",
    "invocation_enqueue_failed",
    "target_lookup_failed",
    "target_unavailable",
    "unknown",
}


def check_event_fanout_attempt_response_failure_code(value: str) -> EventFanoutAttemptResponseFailureCode:
    if value in EVENT_FANOUT_ATTEMPT_RESPONSE_FAILURE_CODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_FANOUT_ATTEMPT_RESPONSE_FAILURE_CODE_VALUES!r}"
    )
