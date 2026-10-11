from typing import Literal

EventRecoveryExecutionJobHealthStatus = Literal["prolonged_wait", "retention_risk", "unknown", "waiting"]

EVENT_RECOVERY_EXECUTION_JOB_HEALTH_STATUS_VALUES: set[EventRecoveryExecutionJobHealthStatus] = {
    "prolonged_wait",
    "retention_risk",
    "unknown",
    "waiting",
}


def check_event_recovery_execution_job_health_status(value: str) -> EventRecoveryExecutionJobHealthStatus:
    if value in EVENT_RECOVERY_EXECUTION_JOB_HEALTH_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_EXECUTION_JOB_HEALTH_STATUS_VALUES!r}"
    )
