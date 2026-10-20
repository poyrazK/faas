from typing import Literal

EventRecoveryNotificationsHealthCoverage = Literal["bounded_retained_notification_jobs"]

EVENT_RECOVERY_NOTIFICATIONS_HEALTH_COVERAGE_VALUES: set[EventRecoveryNotificationsHealthCoverage] = {
    "bounded_retained_notification_jobs",
}


def check_event_recovery_notifications_health_coverage(value: str) -> EventRecoveryNotificationsHealthCoverage:
    if value in EVENT_RECOVERY_NOTIFICATIONS_HEALTH_COVERAGE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_NOTIFICATIONS_HEALTH_COVERAGE_VALUES!r}"
    )
