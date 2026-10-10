from typing import Literal

EventRecoveryNotificationRetryTargetKind = Literal["admission", "execution"]

EVENT_RECOVERY_NOTIFICATION_RETRY_TARGET_KIND_VALUES: set[EventRecoveryNotificationRetryTargetKind] = {
    "admission",
    "execution",
}


def check_event_recovery_notification_retry_target_kind(value: str) -> EventRecoveryNotificationRetryTargetKind:
    if value in EVENT_RECOVERY_NOTIFICATION_RETRY_TARGET_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_NOTIFICATION_RETRY_TARGET_KIND_VALUES!r}"
    )
