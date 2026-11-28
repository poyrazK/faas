from typing import Literal

ReplayEventFanoutFailureResponseState = Literal["pending"]

REPLAY_EVENT_FANOUT_FAILURE_RESPONSE_STATE_VALUES: set[ReplayEventFanoutFailureResponseState] = {
    "pending",
}


def check_replay_event_fanout_failure_response_state(value: str) -> ReplayEventFanoutFailureResponseState:
    if value in REPLAY_EVENT_FANOUT_FAILURE_RESPONSE_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REPLAY_EVENT_FANOUT_FAILURE_RESPONSE_STATE_VALUES!r}"
    )
