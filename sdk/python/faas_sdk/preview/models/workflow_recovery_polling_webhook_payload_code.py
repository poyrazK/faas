from typing import Literal

WorkflowRecoveryPollingWebhookPayloadCode = Literal[
    "invalid_observation_data", "invalid_policy_authority", "invalid_polling_data", "invalid_recovery_identity"
]

WORKFLOW_RECOVERY_POLLING_WEBHOOK_PAYLOAD_CODE_VALUES: set[WorkflowRecoveryPollingWebhookPayloadCode] = {
    "invalid_observation_data",
    "invalid_policy_authority",
    "invalid_polling_data",
    "invalid_recovery_identity",
}


def check_workflow_recovery_polling_webhook_payload_code(value: str) -> WorkflowRecoveryPollingWebhookPayloadCode:
    if value in WORKFLOW_RECOVERY_POLLING_WEBHOOK_PAYLOAD_CODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_POLLING_WEBHOOK_PAYLOAD_CODE_VALUES!r}"
    )
