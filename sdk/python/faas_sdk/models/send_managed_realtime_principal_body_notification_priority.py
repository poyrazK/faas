from typing import Literal

SendManagedRealtimePrincipalBodyNotificationPriority = Literal["low", "normal", "urgent"]

SEND_MANAGED_REALTIME_PRINCIPAL_BODY_NOTIFICATION_PRIORITY_VALUES: set[
    SendManagedRealtimePrincipalBodyNotificationPriority
] = {
    "low",
    "normal",
    "urgent",
}


def check_send_managed_realtime_principal_body_notification_priority(
    value: str,
) -> SendManagedRealtimePrincipalBodyNotificationPriority:
    if value in SEND_MANAGED_REALTIME_PRINCIPAL_BODY_NOTIFICATION_PRIORITY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SEND_MANAGED_REALTIME_PRINCIPAL_BODY_NOTIFICATION_PRIORITY_VALUES!r}"
    )
