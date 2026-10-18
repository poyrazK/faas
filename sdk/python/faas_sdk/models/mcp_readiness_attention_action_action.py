from typing import Literal

MCPReadinessAttentionActionAction = Literal["acknowledge", "configure_reminders", "release"]

MCP_READINESS_ATTENTION_ACTION_ACTION_VALUES: set[MCPReadinessAttentionActionAction] = {
    "acknowledge",
    "configure_reminders",
    "release",
}


def check_mcp_readiness_attention_action_action(value: str) -> MCPReadinessAttentionActionAction:
    if value in MCP_READINESS_ATTENTION_ACTION_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_READINESS_ATTENTION_ACTION_ACTION_VALUES!r}")
