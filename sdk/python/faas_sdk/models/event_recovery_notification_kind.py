from typing import Literal

EventRecoveryNotificationKind = Literal["admission", "execution"]

EVENT_RECOVERY_NOTIFICATION_KIND_VALUES: set[EventRecoveryNotificationKind] = {
    "admission",
    "execution",
}


def check_event_recovery_notification_kind(value: str) -> EventRecoveryNotificationKind:
    if value in EVENT_RECOVERY_NOTIFICATION_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_NOTIFICATION_KIND_VALUES!r}")
