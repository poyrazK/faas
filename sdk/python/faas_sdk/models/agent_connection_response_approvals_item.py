from typing import Literal

AgentConnectionResponseApprovalsItem = Literal["billing", "credentials", "delete", "production"]

AGENT_CONNECTION_RESPONSE_APPROVALS_ITEM_VALUES: set[AgentConnectionResponseApprovalsItem] = {
    "billing",
    "credentials",
    "delete",
    "production",
}


def check_agent_connection_response_approvals_item(value: str) -> AgentConnectionResponseApprovalsItem:
    if value in AGENT_CONNECTION_RESPONSE_APPROVALS_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AGENT_CONNECTION_RESPONSE_APPROVALS_ITEM_VALUES!r}")
