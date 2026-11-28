from typing import Literal

AppResponseStatus = Literal["active", "deleted", "evicted_cold", "undeployed"]

APP_RESPONSE_STATUS_VALUES: set[AppResponseStatus] = {
    "active",
    "deleted",
    "evicted_cold",
    "undeployed",
}


def check_app_response_status(value: str) -> AppResponseStatus:
    if value in APP_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_RESPONSE_STATUS_VALUES!r}")
