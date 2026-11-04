from typing import Literal

AppHealthCheckAction = Literal["configuration", "deployments", "errors", "logs", "metrics"]

APP_HEALTH_CHECK_ACTION_VALUES: set[AppHealthCheckAction] = {
    "configuration",
    "deployments",
    "errors",
    "logs",
    "metrics",
}


def check_app_health_check_action(value: str) -> AppHealthCheckAction:
    if value in APP_HEALTH_CHECK_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_HEALTH_CHECK_ACTION_VALUES!r}")
