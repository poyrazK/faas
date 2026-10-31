from typing import Literal

GithubWebhookDeliveryRecordStatus = Literal["dead", "pending", "processing", "succeeded"]

GITHUB_WEBHOOK_DELIVERY_RECORD_STATUS_VALUES: set[GithubWebhookDeliveryRecordStatus] = {
    "dead",
    "pending",
    "processing",
    "succeeded",
}


def check_github_webhook_delivery_record_status(value: str) -> GithubWebhookDeliveryRecordStatus:
    if value in GITHUB_WEBHOOK_DELIVERY_RECORD_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GITHUB_WEBHOOK_DELIVERY_RECORD_STATUS_VALUES!r}")
