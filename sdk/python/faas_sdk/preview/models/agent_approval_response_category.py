from typing import Literal

AgentApprovalResponseCategory = Literal["billing", "credentials", "delete", "production"]

AGENT_APPROVAL_RESPONSE_CATEGORY_VALUES: set[AgentApprovalResponseCategory] = {
    "billing",
    "credentials",
    "delete",
    "production",
}


def check_agent_approval_response_category(value: str) -> AgentApprovalResponseCategory:
    if value in AGENT_APPROVAL_RESPONSE_CATEGORY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AGENT_APPROVAL_RESPONSE_CATEGORY_VALUES!r}")
