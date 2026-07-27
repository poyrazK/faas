from typing import Literal

StreamAppLogsFollow = Literal[0, 1]

STREAM_APP_LOGS_FOLLOW_VALUES: set[StreamAppLogsFollow] = {
    0,
    1,
}


def check_stream_app_logs_follow(value: int) -> StreamAppLogsFollow:
    if value in STREAM_APP_LOGS_FOLLOW_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {STREAM_APP_LOGS_FOLLOW_VALUES!r}")
