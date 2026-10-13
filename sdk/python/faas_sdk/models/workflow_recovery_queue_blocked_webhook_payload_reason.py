from typing import Literal

WorkflowRecoveryQueueBlockedWebhookPayloadReason = Literal["evidence_unknown"]

WORKFLOW_RECOVERY_QUEUE_BLOCKED_WEBHOOK_PAYLOAD_REASON_VALUES: set[WorkflowRecoveryQueueBlockedWebhookPayloadReason] = {
    "evidence_unknown",
}


def check_workflow_recovery_queue_blocked_webhook_payload_reason(
    value: str,
) -> WorkflowRecoveryQueueBlockedWebhookPayloadReason:
    if value in WORKFLOW_RECOVERY_QUEUE_BLOCKED_WEBHOOK_PAYLOAD_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_QUEUE_BLOCKED_WEBHOOK_PAYLOAD_REASON_VALUES!r}"
    )
