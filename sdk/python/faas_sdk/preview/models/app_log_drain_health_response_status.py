from typing import Literal

AppLogDrainHealthResponseStatus = Literal["degraded", "healthy", "inactive", "unknown"]

APP_LOG_DRAIN_HEALTH_RESPONSE_STATUS_VALUES: set[AppLogDrainHealthResponseStatus] = {
    "degraded",
    "healthy",
    "inactive",
    "unknown",
}


def check_app_log_drain_health_response_status(value: str) -> AppLogDrainHealthResponseStatus:
    if value in APP_LOG_DRAIN_HEALTH_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_LOG_DRAIN_HEALTH_RESPONSE_STATUS_VALUES!r}")
