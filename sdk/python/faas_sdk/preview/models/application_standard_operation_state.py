from typing import Literal

ApplicationStandardOperationState = Literal["blocked", "completed", "failed", "paused", "queued", "running", "waiting"]

APPLICATION_STANDARD_OPERATION_STATE_VALUES: set[ApplicationStandardOperationState] = {
    "blocked",
    "completed",
    "failed",
    "paused",
    "queued",
    "running",
    "waiting",
}


def check_application_standard_operation_state(value: str) -> ApplicationStandardOperationState:
    if value in APPLICATION_STANDARD_OPERATION_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APPLICATION_STANDARD_OPERATION_STATE_VALUES!r}")
