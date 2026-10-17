from typing import Literal

AppWakeHistoryPageMode = Literal["keyset_v1"]

APP_WAKE_HISTORY_PAGE_MODE_VALUES: set[AppWakeHistoryPageMode] = {
    "keyset_v1",
}


def check_app_wake_history_page_mode(value: str) -> AppWakeHistoryPageMode:
    if value in APP_WAKE_HISTORY_PAGE_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_WAKE_HISTORY_PAGE_MODE_VALUES!r}")
