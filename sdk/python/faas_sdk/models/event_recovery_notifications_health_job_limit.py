from typing import Literal

EventRecoveryNotificationsHealthJobLimit = Literal[50]

EVENT_RECOVERY_NOTIFICATIONS_HEALTH_JOB_LIMIT_VALUES: set[EventRecoveryNotificationsHealthJobLimit] = {
    50,
}


def check_event_recovery_notifications_health_job_limit(value: int) -> EventRecoveryNotificationsHealthJobLimit:
    if value in EVENT_RECOVERY_NOTIFICATIONS_HEALTH_JOB_LIMIT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_NOTIFICATIONS_HEALTH_JOB_LIMIT_VALUES!r}"
    )
