from typing import Literal

WorkflowRecoveryQueueBlockedWebhookPayloadState = Literal["blocked", "recovered"]

WORKFLOW_RECOVERY_QUEUE_BLOCKED_WEBHOOK_PAYLOAD_STATE_VALUES: set[WorkflowRecoveryQueueBlockedWebhookPayloadState] = {
    "blocked",
    "recovered",
}


def check_workflow_recovery_queue_blocked_webhook_payload_state(
    value: str,
) -> WorkflowRecoveryQueueBlockedWebhookPayloadState:
    if value in WORKFLOW_RECOVERY_QUEUE_BLOCKED_WEBHOOK_PAYLOAD_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_QUEUE_BLOCKED_WEBHOOK_PAYLOAD_STATE_VALUES!r}"
    )
