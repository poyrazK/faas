from typing import Literal

EventRecoveryNotificationReceiverStatus = Literal[
    "awaiting_relay", "dead", "failed", "in_flight", "pending", "succeeded", "unknown"
]

EVENT_RECOVERY_NOTIFICATION_RECEIVER_STATUS_VALUES: set[EventRecoveryNotificationReceiverStatus] = {
    "awaiting_relay",
    "dead",
    "failed",
    "in_flight",
    "pending",
    "succeeded",
    "unknown",
}


def check_event_recovery_notification_receiver_status(value: str) -> EventRecoveryNotificationReceiverStatus:
    if value in EVENT_RECOVERY_NOTIFICATION_RECEIVER_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_NOTIFICATION_RECEIVER_STATUS_VALUES!r}"
    )
