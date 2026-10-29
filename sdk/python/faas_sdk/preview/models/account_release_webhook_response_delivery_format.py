from typing import Literal

AccountReleaseWebhookResponseDeliveryFormat = Literal["cloudevents", "json"]

ACCOUNT_RELEASE_WEBHOOK_RESPONSE_DELIVERY_FORMAT_VALUES: set[AccountReleaseWebhookResponseDeliveryFormat] = {
    "cloudevents",
    "json",
}


def check_account_release_webhook_response_delivery_format(value: str) -> AccountReleaseWebhookResponseDeliveryFormat:
    if value in ACCOUNT_RELEASE_WEBHOOK_RESPONSE_DELIVERY_FORMAT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ACCOUNT_RELEASE_WEBHOOK_RESPONSE_DELIVERY_FORMAT_VALUES!r}"
    )
