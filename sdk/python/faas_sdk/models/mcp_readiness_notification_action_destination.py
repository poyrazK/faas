from typing import Literal

MCPReadinessNotificationActionDestination = Literal["fallback", "primary"]

MCP_READINESS_NOTIFICATION_ACTION_DESTINATION_VALUES: set[MCPReadinessNotificationActionDestination] = {
    "fallback",
    "primary",
}


def check_mcp_readiness_notification_action_destination(value: str) -> MCPReadinessNotificationActionDestination:
    if value in MCP_READINESS_NOTIFICATION_ACTION_DESTINATION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_READINESS_NOTIFICATION_ACTION_DESTINATION_VALUES!r}"
    )
