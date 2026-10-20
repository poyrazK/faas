from typing import Literal

EventRecoveryNotificationAcknowledgementStatus = Literal[
    "acknowledged", "no_receivers", "not_applicable", "unacknowledged", "unknown"
]

EVENT_RECOVERY_NOTIFICATION_ACKNOWLEDGEMENT_STATUS_VALUES: set[EventRecoveryNotificationAcknowledgementStatus] = {
    "acknowledged",
    "no_receivers",
    "not_applicable",
    "unacknowledged",
    "unknown",
}


def check_event_recovery_notification_acknowledgement_status(
    value: str,
) -> EventRecoveryNotificationAcknowledgementStatus:
    if value in EVENT_RECOVERY_NOTIFICATION_ACKNOWLEDGEMENT_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_NOTIFICATION_ACKNOWLEDGEMENT_STATUS_VALUES!r}"
    )
