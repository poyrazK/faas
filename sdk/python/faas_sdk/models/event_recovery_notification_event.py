from typing import Literal

EventRecoveryNotificationEvent = Literal[
    "event_recovery.cancelled",
    "event_recovery.completed",
    "event_recovery.execution_finished",
    "event_recovery.expired",
]

EVENT_RECOVERY_NOTIFICATION_EVENT_VALUES: set[EventRecoveryNotificationEvent] = {
    "event_recovery.cancelled",
    "event_recovery.completed",
    "event_recovery.execution_finished",
    "event_recovery.expired",
}


def check_event_recovery_notification_event(value: str) -> EventRecoveryNotificationEvent:
    if value in EVENT_RECOVERY_NOTIFICATION_EVENT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_NOTIFICATION_EVENT_VALUES!r}")
