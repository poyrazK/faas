from typing import Literal

AgentApprovalResponseStatus = Literal["approved", "consumed", "denied", "expired", "pending"]

AGENT_APPROVAL_RESPONSE_STATUS_VALUES: set[AgentApprovalResponseStatus] = {
    "approved",
    "consumed",
    "denied",
    "expired",
    "pending",
}


def check_agent_approval_response_status(value: str) -> AgentApprovalResponseStatus:
    if value in AGENT_APPROVAL_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AGENT_APPROVAL_RESPONSE_STATUS_VALUES!r}")
