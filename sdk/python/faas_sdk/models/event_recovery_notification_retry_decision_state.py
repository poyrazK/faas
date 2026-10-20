from typing import Literal

EventRecoveryNotificationRetryDecisionState = Literal["queued", "skipped"]

EVENT_RECOVERY_NOTIFICATION_RETRY_DECISION_STATE_VALUES: set[EventRecoveryNotificationRetryDecisionState] = {
    "queued",
    "skipped",
}


def check_event_recovery_notification_retry_decision_state(value: str) -> EventRecoveryNotificationRetryDecisionState:
    if value in EVENT_RECOVERY_NOTIFICATION_RETRY_DECISION_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_NOTIFICATION_RETRY_DECISION_STATE_VALUES!r}"
    )
