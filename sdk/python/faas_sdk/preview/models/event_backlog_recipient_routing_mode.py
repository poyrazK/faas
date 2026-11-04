from typing import Literal

EventBacklogRecipientRoutingMode = Literal["event", "recipient"]

EVENT_BACKLOG_RECIPIENT_ROUTING_MODE_VALUES: set[EventBacklogRecipientRoutingMode] = {
    "event",
    "recipient",
}


def check_event_backlog_recipient_routing_mode(value: str) -> EventBacklogRecipientRoutingMode:
    if value in EVENT_BACKLOG_RECIPIENT_ROUTING_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_BACKLOG_RECIPIENT_ROUTING_MODE_VALUES!r}")
