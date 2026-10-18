from typing import Literal

CreateNotificationChannelRequestPagerdutyRegion = Literal["eu", "us"]

CREATE_NOTIFICATION_CHANNEL_REQUEST_PAGERDUTY_REGION_VALUES: set[CreateNotificationChannelRequestPagerdutyRegion] = {
    "eu",
    "us",
}


def check_create_notification_channel_request_pagerduty_region(
    value: str,
) -> CreateNotificationChannelRequestPagerdutyRegion:
    if value in CREATE_NOTIFICATION_CHANNEL_REQUEST_PAGERDUTY_REGION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_NOTIFICATION_CHANNEL_REQUEST_PAGERDUTY_REGION_VALUES!r}"
    )
