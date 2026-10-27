from typing import Literal

EventRecoveryExecutionState = Literal[
    "cancelled",
    "dead_lettered",
    "expired",
    "failed",
    "queued",
    "retrying",
    "running",
    "succeeded",
    "superseded",
    "unknown",
]

EVENT_RECOVERY_EXECUTION_STATE_VALUES: set[EventRecoveryExecutionState] = {
    "cancelled",
    "dead_lettered",
    "expired",
    "failed",
    "queued",
    "retrying",
    "running",
    "succeeded",
    "superseded",
    "unknown",
}


def check_event_recovery_execution_state(value: str) -> EventRecoveryExecutionState:
    if value in EVENT_RECOVERY_EXECUTION_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_EXECUTION_STATE_VALUES!r}")
