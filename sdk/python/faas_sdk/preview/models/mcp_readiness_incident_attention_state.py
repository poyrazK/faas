from typing import Literal

MCPReadinessIncidentAttentionState = Literal["acknowledged", "expired", "resolved", "unacknowledged"]

MCP_READINESS_INCIDENT_ATTENTION_STATE_VALUES: set[MCPReadinessIncidentAttentionState] = {
    "acknowledged",
    "expired",
    "resolved",
    "unacknowledged",
}


def check_mcp_readiness_incident_attention_state(value: str) -> MCPReadinessIncidentAttentionState:
    if value in MCP_READINESS_INCIDENT_ATTENTION_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_READINESS_INCIDENT_ATTENTION_STATE_VALUES!r}")
