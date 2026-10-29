from typing import Literal

UpdateAppWebhookRequestDeliveryFormat = Literal["cloudevents", "datadog", "json"]

UPDATE_APP_WEBHOOK_REQUEST_DELIVERY_FORMAT_VALUES: set[UpdateAppWebhookRequestDeliveryFormat] = {
    "cloudevents",
    "datadog",
    "json",
}


def check_update_app_webhook_request_delivery_format(value: str) -> UpdateAppWebhookRequestDeliveryFormat:
    if value in UPDATE_APP_WEBHOOK_REQUEST_DELIVERY_FORMAT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPDATE_APP_WEBHOOK_REQUEST_DELIVERY_FORMAT_VALUES!r}"
    )
