from typing import Literal

CreateNotificationChannelRequestKind = Literal["email", "pagerduty", "slack"]

CREATE_NOTIFICATION_CHANNEL_REQUEST_KIND_VALUES: set[CreateNotificationChannelRequestKind] = {
    "email",
    "pagerduty",
    "slack",
}


def check_create_notification_channel_request_kind(value: str) -> CreateNotificationChannelRequestKind:
    if value in CREATE_NOTIFICATION_CHANNEL_REQUEST_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_NOTIFICATION_CHANNEL_REQUEST_KIND_VALUES!r}")
