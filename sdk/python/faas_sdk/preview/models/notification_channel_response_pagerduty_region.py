from typing import Literal

NotificationChannelResponsePagerdutyRegion = Literal["eu", "us"]

NOTIFICATION_CHANNEL_RESPONSE_PAGERDUTY_REGION_VALUES: set[NotificationChannelResponsePagerdutyRegion] = {
    "eu",
    "us",
}


def check_notification_channel_response_pagerduty_region(value: str) -> NotificationChannelResponsePagerdutyRegion:
    if value in NOTIFICATION_CHANNEL_RESPONSE_PAGERDUTY_REGION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {NOTIFICATION_CHANNEL_RESPONSE_PAGERDUTY_REGION_VALUES!r}"
    )
