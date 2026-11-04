from typing import Literal

RetryGithubWebhookDeliveryConfirm = Literal["true"]

RETRY_GITHUB_WEBHOOK_DELIVERY_CONFIRM_VALUES: set[RetryGithubWebhookDeliveryConfirm] = {
    "true",
}


def check_retry_github_webhook_delivery_confirm(value: str) -> RetryGithubWebhookDeliveryConfirm:
    if value in RETRY_GITHUB_WEBHOOK_DELIVERY_CONFIRM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RETRY_GITHUB_WEBHOOK_DELIVERY_CONFIRM_VALUES!r}")
