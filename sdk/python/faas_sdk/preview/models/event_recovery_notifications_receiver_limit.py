from typing import Literal

EventRecoveryNotificationsReceiverLimit = Literal[100]

EVENT_RECOVERY_NOTIFICATIONS_RECEIVER_LIMIT_VALUES: set[EventRecoveryNotificationsReceiverLimit] = {
    100,
}


def check_event_recovery_notifications_receiver_limit(value: int) -> EventRecoveryNotificationsReceiverLimit:
    if value in EVENT_RECOVERY_NOTIFICATIONS_RECEIVER_LIMIT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_NOTIFICATIONS_RECEIVER_LIMIT_VALUES!r}"
    )
