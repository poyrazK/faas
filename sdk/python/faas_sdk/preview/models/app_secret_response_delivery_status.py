from typing import Literal

AppSecretResponseDeliveryStatus = Literal["delivered", "failed", "pending"]

APP_SECRET_RESPONSE_DELIVERY_STATUS_VALUES: set[AppSecretResponseDeliveryStatus] = {
    "delivered",
    "failed",
    "pending",
}


def check_app_secret_response_delivery_status(value: str) -> AppSecretResponseDeliveryStatus:
    if value in APP_SECRET_RESPONSE_DELIVERY_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_SECRET_RESPONSE_DELIVERY_STATUS_VALUES!r}")
