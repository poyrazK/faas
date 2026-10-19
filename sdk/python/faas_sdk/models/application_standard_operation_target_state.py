from typing import Literal

ApplicationStandardOperationTargetState = Literal["applying", "blocked", "observed", "persisted", "queued", "skipped"]

APPLICATION_STANDARD_OPERATION_TARGET_STATE_VALUES: set[ApplicationStandardOperationTargetState] = {
    "applying",
    "blocked",
    "observed",
    "persisted",
    "queued",
    "skipped",
}


def check_application_standard_operation_target_state(value: str) -> ApplicationStandardOperationTargetState:
    if value in APPLICATION_STANDARD_OPERATION_TARGET_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APPLICATION_STANDARD_OPERATION_TARGET_STATE_VALUES!r}"
    )
