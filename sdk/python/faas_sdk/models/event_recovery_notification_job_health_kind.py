from typing import Literal

EventRecoveryNotificationJobHealthKind = Literal["admission", "execution"]

EVENT_RECOVERY_NOTIFICATION_JOB_HEALTH_KIND_VALUES: set[EventRecoveryNotificationJobHealthKind] = {
    "admission",
    "execution",
}


def check_event_recovery_notification_job_health_kind(value: str) -> EventRecoveryNotificationJobHealthKind:
    if value in EVENT_RECOVERY_NOTIFICATION_JOB_HEALTH_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_NOTIFICATION_JOB_HEALTH_KIND_VALUES!r}"
    )
