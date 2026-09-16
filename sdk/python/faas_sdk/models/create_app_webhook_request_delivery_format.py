from typing import Literal

CreateAppWebhookRequestDeliveryFormat = Literal["cloudevents", "json"]

CREATE_APP_WEBHOOK_REQUEST_DELIVERY_FORMAT_VALUES: set[CreateAppWebhookRequestDeliveryFormat] = {
    "cloudevents",
    "json",
}


def check_create_app_webhook_request_delivery_format(value: str) -> CreateAppWebhookRequestDeliveryFormat:
    if value in CREATE_APP_WEBHOOK_REQUEST_DELIVERY_FORMAT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_APP_WEBHOOK_REQUEST_DELIVERY_FORMAT_VALUES!r}"
    )
