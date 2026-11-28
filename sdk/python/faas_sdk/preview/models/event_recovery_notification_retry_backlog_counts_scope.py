from typing import Literal

EventRecoveryNotificationRetryBacklogCountsScope = Literal["job_page"]

EVENT_RECOVERY_NOTIFICATION_RETRY_BACKLOG_COUNTS_SCOPE_VALUES: set[EventRecoveryNotificationRetryBacklogCountsScope] = {
    "job_page",
}


def check_event_recovery_notification_retry_backlog_counts_scope(
    value: str,
) -> EventRecoveryNotificationRetryBacklogCountsScope:
    if value in EVENT_RECOVERY_NOTIFICATION_RETRY_BACKLOG_COUNTS_SCOPE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_NOTIFICATION_RETRY_BACKLOG_COUNTS_SCOPE_VALUES!r}"
    )
