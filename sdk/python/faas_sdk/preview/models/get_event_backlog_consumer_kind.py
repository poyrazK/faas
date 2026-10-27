from typing import Literal

GetEventBacklogConsumerKind = Literal["application", "workflow"]

GET_EVENT_BACKLOG_CONSUMER_KIND_VALUES: set[GetEventBacklogConsumerKind] = {
    "application",
    "workflow",
}


def check_get_event_backlog_consumer_kind(value: str) -> GetEventBacklogConsumerKind:
    if value in GET_EVENT_BACKLOG_CONSUMER_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_EVENT_BACKLOG_CONSUMER_KIND_VALUES!r}")
