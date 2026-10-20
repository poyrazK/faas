from typing import Literal

AppHealthFindingStatus = Literal["fail", "unknown"]

APP_HEALTH_FINDING_STATUS_VALUES: set[AppHealthFindingStatus] = {
    "fail",
    "unknown",
}


def check_app_health_finding_status(value: str) -> AppHealthFindingStatus:
    if value in APP_HEALTH_FINDING_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_HEALTH_FINDING_STATUS_VALUES!r}")
