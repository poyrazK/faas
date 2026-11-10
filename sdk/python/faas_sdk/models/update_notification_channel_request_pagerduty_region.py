from typing import Literal

UpdateNotificationChannelRequestPagerdutyRegion = Literal["eu", "us"]

UPDATE_NOTIFICATION_CHANNEL_REQUEST_PAGERDUTY_REGION_VALUES: set[UpdateNotificationChannelRequestPagerdutyRegion] = {
    "eu",
    "us",
}


def check_update_notification_channel_request_pagerduty_region(
    value: str,
) -> UpdateNotificationChannelRequestPagerdutyRegion:
    if value in UPDATE_NOTIFICATION_CHANNEL_REQUEST_PAGERDUTY_REGION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPDATE_NOTIFICATION_CHANNEL_REQUEST_PAGERDUTY_REGION_VALUES!r}"
    )
