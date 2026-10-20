from typing import Literal

EventBacklogConsumerConsumerKind = Literal["application", "workflow"]

EVENT_BACKLOG_CONSUMER_CONSUMER_KIND_VALUES: set[EventBacklogConsumerConsumerKind] = {
    "application",
    "workflow",
}


def check_event_backlog_consumer_consumer_kind(value: str) -> EventBacklogConsumerConsumerKind:
    if value in EVENT_BACKLOG_CONSUMER_CONSUMER_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_BACKLOG_CONSUMER_CONSUMER_KIND_VALUES!r}")
