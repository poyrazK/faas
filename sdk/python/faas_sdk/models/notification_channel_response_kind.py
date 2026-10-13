from typing import Literal

NotificationChannelResponseKind = Literal["email", "pagerduty", "slack"]

NOTIFICATION_CHANNEL_RESPONSE_KIND_VALUES: set[NotificationChannelResponseKind] = {
    "email",
    "pagerduty",
    "slack",
}


def check_notification_channel_response_kind(value: str) -> NotificationChannelResponseKind:
    if value in NOTIFICATION_CHANNEL_RESPONSE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {NOTIFICATION_CHANNEL_RESPONSE_KIND_VALUES!r}")
