from typing import Literal

ScopedAppSecretResponseDeliveryStatus = Literal["delivered", "failed", "pending"]

SCOPED_APP_SECRET_RESPONSE_DELIVERY_STATUS_VALUES: set[ScopedAppSecretResponseDeliveryStatus] = {
    "delivered",
    "failed",
    "pending",
}


def check_scoped_app_secret_response_delivery_status(value: str) -> ScopedAppSecretResponseDeliveryStatus:
    if value in SCOPED_APP_SECRET_RESPONSE_DELIVERY_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SCOPED_APP_SECRET_RESPONSE_DELIVERY_STATUS_VALUES!r}"
    )
