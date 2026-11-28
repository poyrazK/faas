from typing import Literal

AppOperationCheckStatus = Literal["allowed", "blocked", "unknown"]

APP_OPERATION_CHECK_STATUS_VALUES: set[AppOperationCheckStatus] = {
    "allowed",
    "blocked",
    "unknown",
}


def check_app_operation_check_status(value: str) -> AppOperationCheckStatus:
    if value in APP_OPERATION_CHECK_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_OPERATION_CHECK_STATUS_VALUES!r}")
