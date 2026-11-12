from typing import Literal

AgentApprovalResponseCategory = Literal["billing", "budget", "credentials", "delete", "production", "undo"]

AGENT_APPROVAL_RESPONSE_CATEGORY_VALUES: set[AgentApprovalResponseCategory] = {
    "billing",
    "budget",
    "credentials",
    "delete",
    "production",
    "undo",
}


def check_agent_approval_response_category(value: str) -> AgentApprovalResponseCategory:
    if value in AGENT_APPROVAL_RESPONSE_CATEGORY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AGENT_APPROVAL_RESPONSE_CATEGORY_VALUES!r}")
