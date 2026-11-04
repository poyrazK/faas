from typing import Literal

WebhookAutomationReceiptResponseRoutingStatus = Literal["enqueued", "failed", "filtered", "ignored", "pending"]

WEBHOOK_AUTOMATION_RECEIPT_RESPONSE_ROUTING_STATUS_VALUES: set[WebhookAutomationReceiptResponseRoutingStatus] = {
    "enqueued",
    "failed",
    "filtered",
    "ignored",
    "pending",
}


def check_webhook_automation_receipt_response_routing_status(
    value: str,
) -> WebhookAutomationReceiptResponseRoutingStatus:
    if value in WEBHOOK_AUTOMATION_RECEIPT_RESPONSE_ROUTING_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WEBHOOK_AUTOMATION_RECEIPT_RESPONSE_ROUTING_STATUS_VALUES!r}"
    )
