from typing import Literal

AppForkResponseStatus = Literal["cancelled", "expired", "failed", "queued", "restoring", "running"]

APP_FORK_RESPONSE_STATUS_VALUES: set[AppForkResponseStatus] = {
    "cancelled",
    "expired",
    "failed",
    "queued",
    "restoring",
    "running",
}


def check_app_fork_response_status(value: str) -> AppForkResponseStatus:
    if value in APP_FORK_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_FORK_RESPONSE_STATUS_VALUES!r}")
