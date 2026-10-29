from typing import Literal

MCPReadinessIncidentAttentionReminderPolicySource = Literal["incident_override", "legacy", "rule_default"]

MCP_READINESS_INCIDENT_ATTENTION_REMINDER_POLICY_SOURCE_VALUES: set[
    MCPReadinessIncidentAttentionReminderPolicySource
] = {
    "incident_override",
    "legacy",
    "rule_default",
}


def check_mcp_readiness_incident_attention_reminder_policy_source(
    value: str,
) -> MCPReadinessIncidentAttentionReminderPolicySource:
    if value in MCP_READINESS_INCIDENT_ATTENTION_REMINDER_POLICY_SOURCE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_READINESS_INCIDENT_ATTENTION_REMINDER_POLICY_SOURCE_VALUES!r}"
    )
