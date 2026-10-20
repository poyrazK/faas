from typing import Literal

EventRecoveryJobHealthStatus = Literal[
    "capacity_wait", "expired", "finishing", "legacy_claim_wait", "paced", "paused", "running", "stalled"
]

EVENT_RECOVERY_JOB_HEALTH_STATUS_VALUES: set[EventRecoveryJobHealthStatus] = {
    "capacity_wait",
    "expired",
    "finishing",
    "legacy_claim_wait",
    "paced",
    "paused",
    "running",
    "stalled",
}


def check_event_recovery_job_health_status(value: str) -> EventRecoveryJobHealthStatus:
    if value in EVENT_RECOVERY_JOB_HEALTH_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_JOB_HEALTH_STATUS_VALUES!r}")
