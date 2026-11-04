from typing import Literal

StreamAppLogsArchive = Literal[0, 1]

STREAM_APP_LOGS_ARCHIVE_VALUES: set[StreamAppLogsArchive] = {
    0,
    1,
}


def check_stream_app_logs_archive(value: int) -> StreamAppLogsArchive:
    if value in STREAM_APP_LOGS_ARCHIVE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {STREAM_APP_LOGS_ARCHIVE_VALUES!r}")
