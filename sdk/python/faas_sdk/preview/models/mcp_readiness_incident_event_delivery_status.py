from typing import Literal

MCPReadinessIncidentEventDeliveryStatus = Literal[
    "abandoned", "delivered", "delivering", "failed", "pending", "suppressed"
]

MCP_READINESS_INCIDENT_EVENT_DELIVERY_STATUS_VALUES: set[MCPReadinessIncidentEventDeliveryStatus] = {
    "abandoned",
    "delivered",
    "delivering",
    "failed",
    "pending",
    "suppressed",
}


def check_mcp_readiness_incident_event_delivery_status(value: str) -> MCPReadinessIncidentEventDeliveryStatus:
    if value in MCP_READINESS_INCIDENT_EVENT_DELIVERY_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_READINESS_INCIDENT_EVENT_DELIVERY_STATUS_VALUES!r}"
    )
