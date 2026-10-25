from typing import Literal

AppHealthHistoryPageScope = Literal["default"]

APP_HEALTH_HISTORY_PAGE_SCOPE_VALUES: set[AppHealthHistoryPageScope] = {
    "default",
}


def check_app_health_history_page_scope(value: str) -> AppHealthHistoryPageScope:
    if value in APP_HEALTH_HISTORY_PAGE_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_HEALTH_HISTORY_PAGE_SCOPE_VALUES!r}")
