from typing import Literal

WorkflowRecoveryPollingWebhookPayloadScope = Literal["automatic_policy", "manual_initial"]

WORKFLOW_RECOVERY_POLLING_WEBHOOK_PAYLOAD_SCOPE_VALUES: set[WorkflowRecoveryPollingWebhookPayloadScope] = {
    "automatic_policy",
    "manual_initial",
}


def check_workflow_recovery_polling_webhook_payload_scope(value: str) -> WorkflowRecoveryPollingWebhookPayloadScope:
    if value in WORKFLOW_RECOVERY_POLLING_WEBHOOK_PAYLOAD_SCOPE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_POLLING_WEBHOOK_PAYLOAD_SCOPE_VALUES!r}"
    )
