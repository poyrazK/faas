from typing import Literal

EventBacklogRecipientConsumerKind = Literal["application", "workflow"]

EVENT_BACKLOG_RECIPIENT_CONSUMER_KIND_VALUES: set[EventBacklogRecipientConsumerKind] = {
    "application",
    "workflow",
}


def check_event_backlog_recipient_consumer_kind(value: str) -> EventBacklogRecipientConsumerKind:
    if value in EVENT_BACKLOG_RECIPIENT_CONSUMER_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_BACKLOG_RECIPIENT_CONSUMER_KIND_VALUES!r}")
