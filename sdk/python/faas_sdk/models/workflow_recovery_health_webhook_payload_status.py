from typing import Literal

WorkflowRecoveryHealthWebhookPayloadStatus = Literal["blocked", "healthy", "inactive", "paused", "stalled", "unknown"]

WORKFLOW_RECOVERY_HEALTH_WEBHOOK_PAYLOAD_STATUS_VALUES: set[WorkflowRecoveryHealthWebhookPayloadStatus] = {
    "blocked",
    "healthy",
    "inactive",
    "paused",
    "stalled",
    "unknown",
}


def check_workflow_recovery_health_webhook_payload_status(value: str) -> WorkflowRecoveryHealthWebhookPayloadStatus:
    if value in WORKFLOW_RECOVERY_HEALTH_WEBHOOK_PAYLOAD_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_HEALTH_WEBHOOK_PAYLOAD_STATUS_VALUES!r}"
    )
