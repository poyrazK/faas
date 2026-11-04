from typing import Literal

EventRecoveryExecutionHealthJobLimit = Literal[50]

EVENT_RECOVERY_EXECUTION_HEALTH_JOB_LIMIT_VALUES: set[EventRecoveryExecutionHealthJobLimit] = {
    50,
}


def check_event_recovery_execution_health_job_limit(value: int) -> EventRecoveryExecutionHealthJobLimit:
    if value in EVENT_RECOVERY_EXECUTION_HEALTH_JOB_LIMIT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_EXECUTION_HEALTH_JOB_LIMIT_VALUES!r}")
