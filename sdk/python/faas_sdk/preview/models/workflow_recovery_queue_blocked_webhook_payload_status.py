from typing import Literal

WorkflowRecoveryQueueBlockedWebhookPayloadStatus = Literal["applied", "pending"]

WORKFLOW_RECOVERY_QUEUE_BLOCKED_WEBHOOK_PAYLOAD_STATUS_VALUES: set[WorkflowRecoveryQueueBlockedWebhookPayloadStatus] = {
    "applied",
    "pending",
}


def check_workflow_recovery_queue_blocked_webhook_payload_status(
    value: str,
) -> WorkflowRecoveryQueueBlockedWebhookPayloadStatus:
    if value in WORKFLOW_RECOVERY_QUEUE_BLOCKED_WEBHOOK_PAYLOAD_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_QUEUE_BLOCKED_WEBHOOK_PAYLOAD_STATUS_VALUES!r}"
    )
