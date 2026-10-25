from typing import Literal

GetEventBacklogCapacityScope = Literal["account", "app", "consumer"]

GET_EVENT_BACKLOG_CAPACITY_SCOPE_VALUES: set[GetEventBacklogCapacityScope] = {
    "account",
    "app",
    "consumer",
}


def check_get_event_backlog_capacity_scope(value: str) -> GetEventBacklogCapacityScope:
    if value in GET_EVENT_BACKLOG_CAPACITY_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_EVENT_BACKLOG_CAPACITY_SCOPE_VALUES!r}")
