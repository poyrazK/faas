from typing import Literal

WorkflowRecoveryPollingWebhookPayloadState = Literal["failed", "recovered"]

WORKFLOW_RECOVERY_POLLING_WEBHOOK_PAYLOAD_STATE_VALUES: set[WorkflowRecoveryPollingWebhookPayloadState] = {
    "failed",
    "recovered",
}


def check_workflow_recovery_polling_webhook_payload_state(value: str) -> WorkflowRecoveryPollingWebhookPayloadState:
    if value in WORKFLOW_RECOVERY_POLLING_WEBHOOK_PAYLOAD_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_POLLING_WEBHOOK_PAYLOAD_STATE_VALUES!r}"
    )
