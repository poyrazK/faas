from typing import Literal

EventRecoveryNotificationCaptureStatus = Literal["captured", "not_applicable", "pending", "unknown"]

EVENT_RECOVERY_NOTIFICATION_CAPTURE_STATUS_VALUES: set[EventRecoveryNotificationCaptureStatus] = {
    "captured",
    "not_applicable",
    "pending",
    "unknown",
}


def check_event_recovery_notification_capture_status(value: str) -> EventRecoveryNotificationCaptureStatus:
    if value in EVENT_RECOVERY_NOTIFICATION_CAPTURE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_NOTIFICATION_CAPTURE_STATUS_VALUES!r}"
    )
