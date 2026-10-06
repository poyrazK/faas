from typing import Literal

EventBacklogRecipientCapacityScope = Literal["account", "app", "consumer"]

EVENT_BACKLOG_RECIPIENT_CAPACITY_SCOPE_VALUES: set[EventBacklogRecipientCapacityScope] = {
    "account",
    "app",
    "consumer",
}


def check_event_backlog_recipient_capacity_scope(value: str) -> EventBacklogRecipientCapacityScope:
    if value in EVENT_BACKLOG_RECIPIENT_CAPACITY_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_BACKLOG_RECIPIENT_CAPACITY_SCOPE_VALUES!r}")
