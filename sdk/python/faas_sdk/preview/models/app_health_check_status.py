from typing import Literal

AppHealthCheckStatus = Literal["fail", "not_applicable", "pass", "unknown", "warning"]

APP_HEALTH_CHECK_STATUS_VALUES: set[AppHealthCheckStatus] = {
    "fail",
    "not_applicable",
    "pass",
    "unknown",
    "warning",
}


def check_app_health_check_status(value: str) -> AppHealthCheckStatus:
    if value in APP_HEALTH_CHECK_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_HEALTH_CHECK_STATUS_VALUES!r}")
