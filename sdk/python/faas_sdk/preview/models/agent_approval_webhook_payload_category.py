from typing import Literal

AgentApprovalWebhookPayloadCategory = Literal["billing", "budget", "credentials", "delete", "production", "undo"]

AGENT_APPROVAL_WEBHOOK_PAYLOAD_CATEGORY_VALUES: set[AgentApprovalWebhookPayloadCategory] = {
    "billing",
    "budget",
    "credentials",
    "delete",
    "production",
    "undo",
}


def check_agent_approval_webhook_payload_category(value: str) -> AgentApprovalWebhookPayloadCategory:
    if value in AGENT_APPROVAL_WEBHOOK_PAYLOAD_CATEGORY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AGENT_APPROVAL_WEBHOOK_PAYLOAD_CATEGORY_VALUES!r}")
