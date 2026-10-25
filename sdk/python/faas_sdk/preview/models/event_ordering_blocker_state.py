from typing import Literal

EventOrderingBlockerState = Literal["pending", "processing"]

EVENT_ORDERING_BLOCKER_STATE_VALUES: set[EventOrderingBlockerState] = {
    "pending",
    "processing",
}


def check_event_ordering_blocker_state(value: str) -> EventOrderingBlockerState:
    if value in EVENT_ORDERING_BLOCKER_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_ORDERING_BLOCKER_STATE_VALUES!r}")
