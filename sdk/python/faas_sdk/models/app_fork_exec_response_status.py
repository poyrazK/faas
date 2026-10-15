from typing import Literal

AppForkExecResponseStatus = Literal["failed", "queued", "running", "succeeded", "timed_out"]

APP_FORK_EXEC_RESPONSE_STATUS_VALUES: set[AppForkExecResponseStatus] = {
    "failed",
    "queued",
    "running",
    "succeeded",
    "timed_out",
}


def check_app_fork_exec_response_status(value: str) -> AppForkExecResponseStatus:
    if value in APP_FORK_EXEC_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_FORK_EXEC_RESPONSE_STATUS_VALUES!r}")
