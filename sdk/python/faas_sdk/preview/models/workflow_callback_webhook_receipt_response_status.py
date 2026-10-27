from typing import Literal

WorkflowCallbackWebhookReceiptResponseStatus = Literal["ignored", "received"]

WORKFLOW_CALLBACK_WEBHOOK_RECEIPT_RESPONSE_STATUS_VALUES: set[WorkflowCallbackWebhookReceiptResponseStatus] = {
    "ignored",
    "received",
}


def check_workflow_callback_webhook_receipt_response_status(value: str) -> WorkflowCallbackWebhookReceiptResponseStatus:
    if value in WORKFLOW_CALLBACK_WEBHOOK_RECEIPT_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_CALLBACK_WEBHOOK_RECEIPT_RESPONSE_STATUS_VALUES!r}"
    )
