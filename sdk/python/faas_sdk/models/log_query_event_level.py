from typing import Literal

LogQueryEventLevel = Literal["debug", "error", "fatal", "info", "trace", "warn"]

LOG_QUERY_EVENT_LEVEL_VALUES: set[LogQueryEventLevel] = {
    "debug",
    "error",
    "fatal",
    "info",
    "trace",
    "warn",
}


def check_log_query_event_level(value: str) -> LogQueryEventLevel:
    if value in LOG_QUERY_EVENT_LEVEL_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LOG_QUERY_EVENT_LEVEL_VALUES!r}")
