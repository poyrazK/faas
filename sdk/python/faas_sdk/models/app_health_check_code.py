from typing import Literal

AppHealthCheckCode = Literal[
    "deployment", "latest_deployment", "maintenance", "readiness", "requests", "traffic_readiness", "workload"
]

APP_HEALTH_CHECK_CODE_VALUES: set[AppHealthCheckCode] = {
    "deployment",
    "latest_deployment",
    "maintenance",
    "readiness",
    "requests",
    "traffic_readiness",
    "workload",
}


def check_app_health_check_code(value: str) -> AppHealthCheckCode:
    if value in APP_HEALTH_CHECK_CODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_HEALTH_CHECK_CODE_VALUES!r}")
