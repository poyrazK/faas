from typing import Literal

GetEventBacklogOrigin = Literal["acceptance", "backfill"]

GET_EVENT_BACKLOG_ORIGIN_VALUES: set[GetEventBacklogOrigin] = {
    "acceptance",
    "backfill",
}


def check_get_event_backlog_origin(value: str) -> GetEventBacklogOrigin:
    if value in GET_EVENT_BACKLOG_ORIGIN_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_EVENT_BACKLOG_ORIGIN_VALUES!r}")
