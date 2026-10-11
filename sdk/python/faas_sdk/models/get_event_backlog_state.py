from typing import Literal

GetEventBacklogState = Literal["pending", "processing"]

GET_EVENT_BACKLOG_STATE_VALUES: set[GetEventBacklogState] = {
    "pending",
    "processing",
}


def check_get_event_backlog_state(value: str) -> GetEventBacklogState:
    if value in GET_EVENT_BACKLOG_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_EVENT_BACKLOG_STATE_VALUES!r}")
