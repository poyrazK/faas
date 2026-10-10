from typing import Literal

EventRecoveryExecutionSource = Literal["attempt_history", "invocation", "recovery_result", "unavailable"]

EVENT_RECOVERY_EXECUTION_SOURCE_VALUES: set[EventRecoveryExecutionSource] = {
    "attempt_history",
    "invocation",
    "recovery_result",
    "unavailable",
}


def check_event_recovery_execution_source(value: str) -> EventRecoveryExecutionSource:
    if value in EVENT_RECOVERY_EXECUTION_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_EXECUTION_SOURCE_VALUES!r}")
