from typing import Literal

LogQueryEventStream = Literal["access", "stderr", "stdout", "system"]

LOG_QUERY_EVENT_STREAM_VALUES: set[LogQueryEventStream] = {
    "access",
    "stderr",
    "stdout",
    "system",
}


def check_log_query_event_stream(value: str) -> LogQueryEventStream:
    if value in LOG_QUERY_EVENT_STREAM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LOG_QUERY_EVENT_STREAM_VALUES!r}")
