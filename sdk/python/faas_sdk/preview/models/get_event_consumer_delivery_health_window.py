from typing import Literal

GetEventConsumerDeliveryHealthWindow = Literal["15m", "1h", "24h", "5m", "6h"]

GET_EVENT_CONSUMER_DELIVERY_HEALTH_WINDOW_VALUES: set[GetEventConsumerDeliveryHealthWindow] = {
    "15m",
    "1h",
    "24h",
    "5m",
    "6h",
}


def check_get_event_consumer_delivery_health_window(value: str) -> GetEventConsumerDeliveryHealthWindow:
    if value in GET_EVENT_CONSUMER_DELIVERY_HEALTH_WINDOW_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_EVENT_CONSUMER_DELIVERY_HEALTH_WINDOW_VALUES!r}")
