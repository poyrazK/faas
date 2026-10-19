from typing import Literal

MCPReadinessNotificationActionAction = Literal["abandon", "retry"]

MCP_READINESS_NOTIFICATION_ACTION_ACTION_VALUES: set[MCPReadinessNotificationActionAction] = {
    "abandon",
    "retry",
}


def check_mcp_readiness_notification_action_action(value: str) -> MCPReadinessNotificationActionAction:
    if value in MCP_READINESS_NOTIFICATION_ACTION_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_READINESS_NOTIFICATION_ACTION_ACTION_VALUES!r}")
