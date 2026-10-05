from typing import Literal

AppHealthHistoryEntryPreviousStatus = Literal["degraded", "healthy", "unhealthy", "unknown"]

APP_HEALTH_HISTORY_ENTRY_PREVIOUS_STATUS_VALUES: set[AppHealthHistoryEntryPreviousStatus] = {
    "degraded",
    "healthy",
    "unhealthy",
    "unknown",
}


def check_app_health_history_entry_previous_status(value: str) -> AppHealthHistoryEntryPreviousStatus:
    if value in APP_HEALTH_HISTORY_ENTRY_PREVIOUS_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_HEALTH_HISTORY_ENTRY_PREVIOUS_STATUS_VALUES!r}")
