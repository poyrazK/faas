from typing import Literal

AppLogDrainAnalyticsResponseWindow = Literal["1h", "24h", "30d", "7d"]

APP_LOG_DRAIN_ANALYTICS_RESPONSE_WINDOW_VALUES: set[AppLogDrainAnalyticsResponseWindow] = {
    "1h",
    "24h",
    "30d",
    "7d",
}


def check_app_log_drain_analytics_response_window(value: str) -> AppLogDrainAnalyticsResponseWindow:
    if value in APP_LOG_DRAIN_ANALYTICS_RESPONSE_WINDOW_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_LOG_DRAIN_ANALYTICS_RESPONSE_WINDOW_VALUES!r}")
