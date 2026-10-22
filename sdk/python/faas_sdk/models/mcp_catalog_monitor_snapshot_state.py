from typing import Literal

MCPCatalogMonitorSnapshotState = Literal["disabled", "drift", "healthy", "unavailable"]

MCP_CATALOG_MONITOR_SNAPSHOT_STATE_VALUES: set[MCPCatalogMonitorSnapshotState] = {
    "disabled",
    "drift",
    "healthy",
    "unavailable",
}


def check_mcp_catalog_monitor_snapshot_state(value: str) -> MCPCatalogMonitorSnapshotState:
    if value in MCP_CATALOG_MONITOR_SNAPSHOT_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_CATALOG_MONITOR_SNAPSHOT_STATE_VALUES!r}")
