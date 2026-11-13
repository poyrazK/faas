from typing import Literal

WebhookWorkflowReceiptResponseRoutingStatus = Literal["enqueued", "failed", "filtered", "ignored", "pending"]

WEBHOOK_WORKFLOW_RECEIPT_RESPONSE_ROUTING_STATUS_VALUES: set[WebhookWorkflowReceiptResponseRoutingStatus] = {
    "enqueued",
    "failed",
    "filtered",
    "ignored",
    "pending",
}


def check_webhook_workflow_receipt_response_routing_status(value: str) -> WebhookWorkflowReceiptResponseRoutingStatus:
    if value in WEBHOOK_WORKFLOW_RECEIPT_RESPONSE_ROUTING_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WEBHOOK_WORKFLOW_RECEIPT_RESPONSE_ROUTING_STATUS_VALUES!r}"
    )
