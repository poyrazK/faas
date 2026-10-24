from typing import Literal

AppTaskResponseStatus = Literal["cancelled", "failed", "queued", "restoring", "running", "succeeded", "timed_out"]

APP_TASK_RESPONSE_STATUS_VALUES: set[AppTaskResponseStatus] = {
    "cancelled",
    "failed",
    "queued",
    "restoring",
    "running",
    "succeeded",
    "timed_out",
}


def check_app_task_response_status(value: str) -> AppTaskResponseStatus:
    if value in APP_TASK_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_TASK_RESPONSE_STATUS_VALUES!r}")
