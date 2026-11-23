from typing import Literal

MCPReadinessIncidentEventNotificationReason = Literal["maintenance_ended", "reminder"]

MCP_READINESS_INCIDENT_EVENT_NOTIFICATION_REASON_VALUES: set[MCPReadinessIncidentEventNotificationReason] = {
    "maintenance_ended",
    "reminder",
}


def check_mcp_readiness_incident_event_notification_reason(value: str) -> MCPReadinessIncidentEventNotificationReason:
    if value in MCP_READINESS_INCIDENT_EVENT_NOTIFICATION_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_READINESS_INCIDENT_EVENT_NOTIFICATION_REASON_VALUES!r}"
    )
