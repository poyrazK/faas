from typing import Literal

MCPReadinessIncidentEventSuppressionReason = Literal[
    "acknowledged",
    "incident_resolved",
    "maintenance",
    "maintenance_recovered",
    "reminder_obsolete",
    "reminders_disabled",
]

MCP_READINESS_INCIDENT_EVENT_SUPPRESSION_REASON_VALUES: set[MCPReadinessIncidentEventSuppressionReason] = {
    "acknowledged",
    "incident_resolved",
    "maintenance",
    "maintenance_recovered",
    "reminder_obsolete",
    "reminders_disabled",
}


def check_mcp_readiness_incident_event_suppression_reason(value: str) -> MCPReadinessIncidentEventSuppressionReason:
    if value in MCP_READINESS_INCIDENT_EVENT_SUPPRESSION_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_READINESS_INCIDENT_EVENT_SUPPRESSION_REASON_VALUES!r}"
    )
