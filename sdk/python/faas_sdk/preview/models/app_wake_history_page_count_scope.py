from typing import Literal

AppWakeHistoryPageCountScope = Literal["returned_page"]

APP_WAKE_HISTORY_PAGE_COUNT_SCOPE_VALUES: set[AppWakeHistoryPageCountScope] = {
    "returned_page",
}


def check_app_wake_history_page_count_scope(value: str) -> AppWakeHistoryPageCountScope:
    if value in APP_WAKE_HISTORY_PAGE_COUNT_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_WAKE_HISTORY_PAGE_COUNT_SCOPE_VALUES!r}")
