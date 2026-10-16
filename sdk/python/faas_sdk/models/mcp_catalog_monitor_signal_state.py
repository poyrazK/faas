from typing import Literal

MCPCatalogMonitorSignalState = Literal["disabled", "drift", "healthy", "unavailable"]

MCP_CATALOG_MONITOR_SIGNAL_STATE_VALUES: set[MCPCatalogMonitorSignalState] = {
    "disabled",
    "drift",
    "healthy",
    "unavailable",
}


def check_mcp_catalog_monitor_signal_state(value: str) -> MCPCatalogMonitorSignalState:
    if value in MCP_CATALOG_MONITOR_SIGNAL_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_CATALOG_MONITOR_SIGNAL_STATE_VALUES!r}")
