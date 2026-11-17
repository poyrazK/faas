from typing import Literal

SupportNotificationKind = Literal["assigned", "customer_reply", "staff_reply", "ticket_created"]

SUPPORT_NOTIFICATION_KIND_VALUES: set[SupportNotificationKind] = {
    "assigned",
    "customer_reply",
    "staff_reply",
    "ticket_created",
}


def check_support_notification_kind(value: str) -> SupportNotificationKind:
    if value in SUPPORT_NOTIFICATION_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SUPPORT_NOTIFICATION_KIND_VALUES!r}")
