from typing import Literal

MCPReadinessMonitorStatusState = Literal["degraded", "firing", "healthy", "pending", "stale"]

MCP_READINESS_MONITOR_STATUS_STATE_VALUES: set[MCPReadinessMonitorStatusState] = {
    "degraded",
    "firing",
    "healthy",
    "pending",
    "stale",
}


def check_mcp_readiness_monitor_status_state(value: str) -> MCPReadinessMonitorStatusState:
    if value in MCP_READINESS_MONITOR_STATUS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_READINESS_MONITOR_STATUS_STATE_VALUES!r}")
