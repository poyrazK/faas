from typing import Literal

AgentApprovalWebhookPayloadStatus = Literal["approved", "denied", "pending"]

AGENT_APPROVAL_WEBHOOK_PAYLOAD_STATUS_VALUES: set[AgentApprovalWebhookPayloadStatus] = {
    "approved",
    "denied",
    "pending",
}


def check_agent_approval_webhook_payload_status(value: str) -> AgentApprovalWebhookPayloadStatus:
    if value in AGENT_APPROVAL_WEBHOOK_PAYLOAD_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AGENT_APPROVAL_WEBHOOK_PAYLOAD_STATUS_VALUES!r}")
