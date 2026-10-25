from typing import Literal

EventFanoutAttemptResponseState = Literal["enqueued", "failed", "filtered", "pending"]

EVENT_FANOUT_ATTEMPT_RESPONSE_STATE_VALUES: set[EventFanoutAttemptResponseState] = {
    "enqueued",
    "failed",
    "filtered",
    "pending",
}


def check_event_fanout_attempt_response_state(value: str) -> EventFanoutAttemptResponseState:
    if value in EVENT_FANOUT_ATTEMPT_RESPONSE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_FANOUT_ATTEMPT_RESPONSE_STATE_VALUES!r}")
