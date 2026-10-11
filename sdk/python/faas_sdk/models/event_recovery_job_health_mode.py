from typing import Literal

EventRecoveryJobHealthMode = Literal["execution", "routing"]

EVENT_RECOVERY_JOB_HEALTH_MODE_VALUES: set[EventRecoveryJobHealthMode] = {
    "execution",
    "routing",
}


def check_event_recovery_job_health_mode(value: str) -> EventRecoveryJobHealthMode:
    if value in EVENT_RECOVERY_JOB_HEALTH_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_JOB_HEALTH_MODE_VALUES!r}")
