from typing import Literal

AppWakeHistoryPageSourceStatus = Literal["read_succeeded"]

APP_WAKE_HISTORY_PAGE_SOURCE_STATUS_VALUES: set[AppWakeHistoryPageSourceStatus] = {
    "read_succeeded",
}


def check_app_wake_history_page_source_status(value: str) -> AppWakeHistoryPageSourceStatus:
    if value in APP_WAKE_HISTORY_PAGE_SOURCE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_WAKE_HISTORY_PAGE_SOURCE_STATUS_VALUES!r}")
