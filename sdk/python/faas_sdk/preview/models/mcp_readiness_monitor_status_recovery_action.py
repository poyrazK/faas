from typing import Literal

MCPReadinessMonitorStatusRecoveryAction = Literal["reconnect", "review_binding", "review_connection"]

MCP_READINESS_MONITOR_STATUS_RECOVERY_ACTION_VALUES: set[MCPReadinessMonitorStatusRecoveryAction] = {
    "reconnect",
    "review_binding",
    "review_connection",
}


def check_mcp_readiness_monitor_status_recovery_action(value: str) -> MCPReadinessMonitorStatusRecoveryAction:
    if value in MCP_READINESS_MONITOR_STATUS_RECOVERY_ACTION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_READINESS_MONITOR_STATUS_RECOVERY_ACTION_VALUES!r}"
    )
