from typing import Literal

EventRecoveryNotificationRetryDecisionSummaryStatus = Literal["failed", "inconclusive", "pending", "succeeded"]

EVENT_RECOVERY_NOTIFICATION_RETRY_DECISION_SUMMARY_STATUS_VALUES: set[
    EventRecoveryNotificationRetryDecisionSummaryStatus
] = {
    "failed",
    "inconclusive",
    "pending",
    "succeeded",
}


def check_event_recovery_notification_retry_decision_summary_status(
    value: str,
) -> EventRecoveryNotificationRetryDecisionSummaryStatus:
    if value in EVENT_RECOVERY_NOTIFICATION_RETRY_DECISION_SUMMARY_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_NOTIFICATION_RETRY_DECISION_SUMMARY_STATUS_VALUES!r}"
    )
