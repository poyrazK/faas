from typing import Literal

EventRecoveryJobHealthWaitReason = Literal["capacity", "legacy_claim"]

EVENT_RECOVERY_JOB_HEALTH_WAIT_REASON_VALUES: set[EventRecoveryJobHealthWaitReason] = {
    "capacity",
    "legacy_claim",
}


def check_event_recovery_job_health_wait_reason(value: str) -> EventRecoveryJobHealthWaitReason:
    if value in EVENT_RECOVERY_JOB_HEALTH_WAIT_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_JOB_HEALTH_WAIT_REASON_VALUES!r}")
