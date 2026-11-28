from typing import Literal

LogQueryEventSource = Literal["http", "runtime"]

LOG_QUERY_EVENT_SOURCE_VALUES: set[LogQueryEventSource] = {
    "http",
    "runtime",
}


def check_log_query_event_source(value: str) -> LogQueryEventSource:
    if value in LOG_QUERY_EVENT_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LOG_QUERY_EVENT_SOURCE_VALUES!r}")
