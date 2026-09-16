from typing import Literal

AppWebhookResponseDeliveryFormat = Literal["cloudevents", "json"]

APP_WEBHOOK_RESPONSE_DELIVERY_FORMAT_VALUES: set[AppWebhookResponseDeliveryFormat] = {
    "cloudevents",
    "json",
}


def check_app_webhook_response_delivery_format(value: str) -> AppWebhookResponseDeliveryFormat:
    if value in APP_WEBHOOK_RESPONSE_DELIVERY_FORMAT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_WEBHOOK_RESPONSE_DELIVERY_FORMAT_VALUES!r}")
