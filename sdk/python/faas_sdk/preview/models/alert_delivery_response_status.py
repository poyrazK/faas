from typing import Literal

AlertDeliveryResponseStatus = Literal["delivered", "failed", "pending"]

ALERT_DELIVERY_RESPONSE_STATUS_VALUES: set[AlertDeliveryResponseStatus] = {
    "delivered",
    "failed",
    "pending",
}


def check_alert_delivery_response_status(value: str) -> AlertDeliveryResponseStatus:
    if value in ALERT_DELIVERY_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ALERT_DELIVERY_RESPONSE_STATUS_VALUES!r}")
