from typing import Literal

AgentApprovalWebhookPayloadCategory = Literal["billing", "credentials", "delete", "production"]

AGENT_APPROVAL_WEBHOOK_PAYLOAD_CATEGORY_VALUES: set[AgentApprovalWebhookPayloadCategory] = {
    "billing",
    "credentials",
    "delete",
    "production",
}


def check_agent_approval_webhook_payload_category(value: str) -> AgentApprovalWebhookPayloadCategory:
    if value in AGENT_APPROVAL_WEBHOOK_PAYLOAD_CATEGORY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AGENT_APPROVAL_WEBHOOK_PAYLOAD_CATEGORY_VALUES!r}")
