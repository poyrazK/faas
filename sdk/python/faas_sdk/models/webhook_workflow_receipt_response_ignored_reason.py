from typing import Literal

WebhookWorkflowReceiptResponseIgnoredReason = Literal[
    "event_filtered", "stored_workflow_failure_paused", "stored_workflow_paused", "stored_workflow_unpublished"
]

WEBHOOK_WORKFLOW_RECEIPT_RESPONSE_IGNORED_REASON_VALUES: set[WebhookWorkflowReceiptResponseIgnoredReason] = {
    "event_filtered",
    "stored_workflow_failure_paused",
    "stored_workflow_paused",
    "stored_workflow_unpublished",
}


def check_webhook_workflow_receipt_response_ignored_reason(value: str) -> WebhookWorkflowReceiptResponseIgnoredReason:
    if value in WEBHOOK_WORKFLOW_RECEIPT_RESPONSE_IGNORED_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WEBHOOK_WORKFLOW_RECEIPT_RESPONSE_IGNORED_REASON_VALUES!r}"
    )
