from typing import Literal

ManagedRealtimePrincipalMessageRequestNotificationPriority = Literal["low", "normal", "urgent"]

MANAGED_REALTIME_PRINCIPAL_MESSAGE_REQUEST_NOTIFICATION_PRIORITY_VALUES: set[
    ManagedRealtimePrincipalMessageRequestNotificationPriority
] = {
    "low",
    "normal",
    "urgent",
}


def check_managed_realtime_principal_message_request_notification_priority(
    value: str,
) -> ManagedRealtimePrincipalMessageRequestNotificationPriority:
    if value in MANAGED_REALTIME_PRINCIPAL_MESSAGE_REQUEST_NOTIFICATION_PRIORITY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_REALTIME_PRINCIPAL_MESSAGE_REQUEST_NOTIFICATION_PRIORITY_VALUES!r}"
    )
