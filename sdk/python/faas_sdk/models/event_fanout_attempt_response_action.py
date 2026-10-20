from typing import Literal

EventFanoutAttemptResponseAction = Literal["fanout_attempt", "operator_replay"]

EVENT_FANOUT_ATTEMPT_RESPONSE_ACTION_VALUES: set[EventFanoutAttemptResponseAction] = {
    "fanout_attempt",
    "operator_replay",
}


def check_event_fanout_attempt_response_action(value: str) -> EventFanoutAttemptResponseAction:
    if value in EVENT_FANOUT_ATTEMPT_RESPONSE_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_FANOUT_ATTEMPT_RESPONSE_ACTION_VALUES!r}")
