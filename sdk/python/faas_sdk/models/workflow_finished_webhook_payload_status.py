from typing import Literal

WorkflowFinishedWebhookPayloadStatus = Literal["dead", "failed", "succeeded"]

WORKFLOW_FINISHED_WEBHOOK_PAYLOAD_STATUS_VALUES: set[WorkflowFinishedWebhookPayloadStatus] = {
    "dead",
    "failed",
    "succeeded",
}


def check_workflow_finished_webhook_payload_status(value: str) -> WorkflowFinishedWebhookPayloadStatus:
    if value in WORKFLOW_FINISHED_WEBHOOK_PAYLOAD_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_FINISHED_WEBHOOK_PAYLOAD_STATUS_VALUES!r}")
