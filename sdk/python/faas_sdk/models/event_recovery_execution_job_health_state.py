from typing import Literal

EventRecoveryExecutionJobHealthState = Literal["cancelled", "completed"]

EVENT_RECOVERY_EXECUTION_JOB_HEALTH_STATE_VALUES: set[EventRecoveryExecutionJobHealthState] = {
    "cancelled",
    "completed",
}


def check_event_recovery_execution_job_health_state(value: str) -> EventRecoveryExecutionJobHealthState:
    if value in EVENT_RECOVERY_EXECUTION_JOB_HEALTH_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_EXECUTION_JOB_HEALTH_STATE_VALUES!r}")
