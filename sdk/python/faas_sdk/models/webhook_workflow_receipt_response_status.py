from typing import Literal

WebhookWorkflowReceiptResponseStatus = Literal["accepted", "ignored"]

WEBHOOK_WORKFLOW_RECEIPT_RESPONSE_STATUS_VALUES: set[WebhookWorkflowReceiptResponseStatus] = {
    "accepted",
    "ignored",
}


def check_webhook_workflow_receipt_response_status(value: str) -> WebhookWorkflowReceiptResponseStatus:
    if value in WEBHOOK_WORKFLOW_RECEIPT_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WEBHOOK_WORKFLOW_RECEIPT_RESPONSE_STATUS_VALUES!r}")
