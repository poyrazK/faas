from typing import Literal

EventFanoutAttemptResponseCapacityScope = Literal["account", "app", "consumer"]

EVENT_FANOUT_ATTEMPT_RESPONSE_CAPACITY_SCOPE_VALUES: set[EventFanoutAttemptResponseCapacityScope] = {
    "account",
    "app",
    "consumer",
}


def check_event_fanout_attempt_response_capacity_scope(value: str) -> EventFanoutAttemptResponseCapacityScope:
    if value in EVENT_FANOUT_ATTEMPT_RESPONSE_CAPACITY_SCOPE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_FANOUT_ATTEMPT_RESPONSE_CAPACITY_SCOPE_VALUES!r}"
    )
