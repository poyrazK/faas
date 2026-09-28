from typing import Literal

EventFanoutFailureResponseState = Literal["failed"]

EVENT_FANOUT_FAILURE_RESPONSE_STATE_VALUES: set[EventFanoutFailureResponseState] = {
    "failed",
}


def check_event_fanout_failure_response_state(value: str) -> EventFanoutFailureResponseState:
    if value in EVENT_FANOUT_FAILURE_RESPONSE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_FANOUT_FAILURE_RESPONSE_STATE_VALUES!r}")
