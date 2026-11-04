from typing import Literal

StreamAppLogsLevel = Literal["error", "info", "warn"]

STREAM_APP_LOGS_LEVEL_VALUES: set[StreamAppLogsLevel] = {
    "error",
    "info",
    "warn",
}


def check_stream_app_logs_level(value: str) -> StreamAppLogsLevel:
    if value in STREAM_APP_LOGS_LEVEL_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {STREAM_APP_LOGS_LEVEL_VALUES!r}")
