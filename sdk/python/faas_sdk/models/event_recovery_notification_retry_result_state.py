from typing import Literal

EventRecoveryNotificationRetryResultState = Literal["queued", "skipped"]

EVENT_RECOVERY_NOTIFICATION_RETRY_RESULT_STATE_VALUES: set[EventRecoveryNotificationRetryResultState] = {
    "queued",
    "skipped",
}


def check_event_recovery_notification_retry_result_state(value: str) -> EventRecoveryNotificationRetryResultState:
    if value in EVENT_RECOVERY_NOTIFICATION_RETRY_RESULT_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_NOTIFICATION_RETRY_RESULT_STATE_VALUES!r}"
    )
