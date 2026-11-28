from typing import Literal

EventFanoutAttemptResponseRetryStopReason = Literal["delivery_expired", "max_attempts", "max_duration", "non_retryable"]

EVENT_FANOUT_ATTEMPT_RESPONSE_RETRY_STOP_REASON_VALUES: set[EventFanoutAttemptResponseRetryStopReason] = {
    "delivery_expired",
    "max_attempts",
    "max_duration",
    "non_retryable",
}


def check_event_fanout_attempt_response_retry_stop_reason(value: str) -> EventFanoutAttemptResponseRetryStopReason:
    if value in EVENT_FANOUT_ATTEMPT_RESPONSE_RETRY_STOP_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_FANOUT_ATTEMPT_RESPONSE_RETRY_STOP_REASON_VALUES!r}"
    )
