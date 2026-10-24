from typing import Literal

EventBacklogRecipientState = Literal["pending", "processing"]

EVENT_BACKLOG_RECIPIENT_STATE_VALUES: set[EventBacklogRecipientState] = {
    "pending",
    "processing",
}


def check_event_backlog_recipient_state(value: str) -> EventBacklogRecipientState:
    if value in EVENT_BACKLOG_RECIPIENT_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_BACKLOG_RECIPIENT_STATE_VALUES!r}")
