from typing import Literal

AgentApprovalPendingInfoCategory = Literal["billing", "budget", "credentials", "delete", "production", "undo"]

AGENT_APPROVAL_PENDING_INFO_CATEGORY_VALUES: set[AgentApprovalPendingInfoCategory] = {
    "billing",
    "budget",
    "credentials",
    "delete",
    "production",
    "undo",
}


def check_agent_approval_pending_info_category(value: str) -> AgentApprovalPendingInfoCategory:
    if value in AGENT_APPROVAL_PENDING_INFO_CATEGORY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AGENT_APPROVAL_PENDING_INFO_CATEGORY_VALUES!r}")
