from typing import Literal

AppHealthResponseStatus = Literal["degraded", "healthy", "unhealthy", "unknown"]

APP_HEALTH_RESPONSE_STATUS_VALUES: set[AppHealthResponseStatus] = {
    "degraded",
    "healthy",
    "unhealthy",
    "unknown",
}


def check_app_health_response_status(value: str) -> AppHealthResponseStatus:
    if value in APP_HEALTH_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_HEALTH_RESPONSE_STATUS_VALUES!r}")
