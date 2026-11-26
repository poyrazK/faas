from typing import Literal

MCPReadinessIncidentEventEvent = Literal["alert.fired", "alert.resolved"]

MCP_READINESS_INCIDENT_EVENT_EVENT_VALUES: set[MCPReadinessIncidentEventEvent] = {
    "alert.fired",
    "alert.resolved",
}


def check_mcp_readiness_incident_event_event(value: str) -> MCPReadinessIncidentEventEvent:
    if value in MCP_READINESS_INCIDENT_EVENT_EVENT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_READINESS_INCIDENT_EVENT_EVENT_VALUES!r}")
