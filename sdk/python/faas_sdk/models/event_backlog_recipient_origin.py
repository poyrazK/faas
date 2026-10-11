from typing import Literal

EventBacklogRecipientOrigin = Literal["acceptance", "backfill"]

EVENT_BACKLOG_RECIPIENT_ORIGIN_VALUES: set[EventBacklogRecipientOrigin] = {
    "acceptance",
    "backfill",
}


def check_event_backlog_recipient_origin(value: str) -> EventBacklogRecipientOrigin:
    if value in EVENT_BACKLOG_RECIPIENT_ORIGIN_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_BACKLOG_RECIPIENT_ORIGIN_VALUES!r}")
