from typing import Literal

UpdateAccountReleaseWebhookRequestDeliveryFormat = Literal["cloudevents", "json"]

UPDATE_ACCOUNT_RELEASE_WEBHOOK_REQUEST_DELIVERY_FORMAT_VALUES: set[UpdateAccountReleaseWebhookRequestDeliveryFormat] = {
    "cloudevents",
    "json",
}


def check_update_account_release_webhook_request_delivery_format(
    value: str,
) -> UpdateAccountReleaseWebhookRequestDeliveryFormat:
    if value in UPDATE_ACCOUNT_RELEASE_WEBHOOK_REQUEST_DELIVERY_FORMAT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPDATE_ACCOUNT_RELEASE_WEBHOOK_REQUEST_DELIVERY_FORMAT_VALUES!r}"
    )
