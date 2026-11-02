from typing import Literal

AgentConnectionResponseKind = Literal["agent_key", "hosted_mcp"]

AGENT_CONNECTION_RESPONSE_KIND_VALUES: set[AgentConnectionResponseKind] = {
    "agent_key",
    "hosted_mcp",
}


def check_agent_connection_response_kind(value: str) -> AgentConnectionResponseKind:
    if value in AGENT_CONNECTION_RESPONSE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AGENT_CONNECTION_RESPONSE_KIND_VALUES!r}")
