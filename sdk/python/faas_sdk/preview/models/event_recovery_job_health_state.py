from typing import Literal

EventRecoveryJobHealthState = Literal["paused", "running"]

EVENT_RECOVERY_JOB_HEALTH_STATE_VALUES: set[EventRecoveryJobHealthState] = {
    "paused",
    "running",
}


def check_event_recovery_job_health_state(value: str) -> EventRecoveryJobHealthState:
    if value in EVENT_RECOVERY_JOB_HEALTH_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_JOB_HEALTH_STATE_VALUES!r}")
