from typing import Literal

PublishManagedRealtimeChannelDelivery = Literal["live", "retained"]

PUBLISH_MANAGED_REALTIME_CHANNEL_DELIVERY_VALUES: set[PublishManagedRealtimeChannelDelivery] = {
    "live",
    "retained",
}


def check_publish_managed_realtime_channel_delivery(value: str) -> PublishManagedRealtimeChannelDelivery:
    if value in PUBLISH_MANAGED_REALTIME_CHANNEL_DELIVERY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PUBLISH_MANAGED_REALTIME_CHANNEL_DELIVERY_VALUES!r}")
