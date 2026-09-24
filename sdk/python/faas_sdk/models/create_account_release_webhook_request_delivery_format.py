from typing import Literal

CreateAccountReleaseWebhookRequestDeliveryFormat = Literal["cloudevents", "json"]

CREATE_ACCOUNT_RELEASE_WEBHOOK_REQUEST_DELIVERY_FORMAT_VALUES: set[CreateAccountReleaseWebhookRequestDeliveryFormat] = {
    "cloudevents",
    "json",
}


def check_create_account_release_webhook_request_delivery_format(
    value: str,
) -> CreateAccountReleaseWebhookRequestDeliveryFormat:
    if value in CREATE_ACCOUNT_RELEASE_WEBHOOK_REQUEST_DELIVERY_FORMAT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_ACCOUNT_RELEASE_WEBHOOK_REQUEST_DELIVERY_FORMAT_VALUES!r}"
    )
