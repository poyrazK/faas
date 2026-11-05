from typing import Literal

EventRecoveryJobState = Literal["cancelled", "completed", "paused", "running"]

EVENT_RECOVERY_JOB_STATE_VALUES: set[EventRecoveryJobState] = {
    "cancelled",
    "completed",
    "paused",
    "running",
}


def check_event_recovery_job_state(value: str) -> EventRecoveryJobState:
    if value in EVENT_RECOVERY_JOB_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_JOB_STATE_VALUES!r}")
