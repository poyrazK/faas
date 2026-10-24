from typing import Literal

GetAppLogDrainAnalyticsWindow = Literal["1h", "24h", "30d", "7d"]

GET_APP_LOG_DRAIN_ANALYTICS_WINDOW_VALUES: set[GetAppLogDrainAnalyticsWindow] = {
    "1h",
    "24h",
    "30d",
    "7d",
}


def check_get_app_log_drain_analytics_window(value: str) -> GetAppLogDrainAnalyticsWindow:
    if value in GET_APP_LOG_DRAIN_ANALYTICS_WINDOW_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_APP_LOG_DRAIN_ANALYTICS_WINDOW_VALUES!r}")
