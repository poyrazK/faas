from typing import Literal

AgentApprovalPendingInfoStatus = Literal["denied", "expired"]

AGENT_APPROVAL_PENDING_INFO_STATUS_VALUES: set[AgentApprovalPendingInfoStatus] = {
    "denied",
    "expired",
}


def check_agent_approval_pending_info_status(value: str) -> AgentApprovalPendingInfoStatus:
    if value in AGENT_APPROVAL_PENDING_INFO_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AGENT_APPROVAL_PENDING_INFO_STATUS_VALUES!r}")
