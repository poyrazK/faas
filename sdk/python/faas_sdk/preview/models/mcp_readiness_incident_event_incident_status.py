from typing import Literal

MCPReadinessIncidentEventIncidentStatus = Literal["open", "resolved"]

MCP_READINESS_INCIDENT_EVENT_INCIDENT_STATUS_VALUES: set[MCPReadinessIncidentEventIncidentStatus] = {
    "open",
    "resolved",
}


def check_mcp_readiness_incident_event_incident_status(value: str) -> MCPReadinessIncidentEventIncidentStatus:
    if value in MCP_READINESS_INCIDENT_EVENT_INCIDENT_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_READINESS_INCIDENT_EVENT_INCIDENT_STATUS_VALUES!r}"
    )
