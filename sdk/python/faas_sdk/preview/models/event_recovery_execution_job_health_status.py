from typing import Literal

EventRecoveryExecutionJobHealthStatus = Literal[
    "capacity_wait",
    "deadline_exceeded",
    "prolonged_wait",
    "retention_risk",
    "scheduled_wait",
    "stalled",
    "unknown",
    "waiting",
]

EVENT_RECOVERY_EXECUTION_JOB_HEALTH_STATUS_VALUES: set[EventRecoveryExecutionJobHealthStatus] = {
    "capacity_wait",
    "deadline_exceeded",
    "prolonged_wait",
    "retention_risk",
    "scheduled_wait",
    "stalled",
    "unknown",
    "waiting",
}


def check_event_recovery_execution_job_health_status(value: str) -> EventRecoveryExecutionJobHealthStatus:
    if value in EVENT_RECOVERY_EXECUTION_JOB_HEALTH_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_EXECUTION_JOB_HEALTH_STATUS_VALUES!r}"
    )
