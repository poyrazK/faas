from typing import Literal

AppWebhookDeliveryResponseStatus = Literal["dead", "failed", "in_flight", "pending", "succeeded"]

APP_WEBHOOK_DELIVERY_RESPONSE_STATUS_VALUES: set[AppWebhookDeliveryResponseStatus] = {
    "dead",
    "failed",
    "in_flight",
    "pending",
    "succeeded",
}


def check_app_webhook_delivery_response_status(value: str) -> AppWebhookDeliveryResponseStatus:
    if value in APP_WEBHOOK_DELIVERY_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_WEBHOOK_DELIVERY_RESPONSE_STATUS_VALUES!r}")
