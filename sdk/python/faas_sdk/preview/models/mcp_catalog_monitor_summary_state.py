from typing import Literal

MCPCatalogMonitorSummaryState = Literal["disabled", "drift", "healthy", "unavailable"]

MCP_CATALOG_MONITOR_SUMMARY_STATE_VALUES: set[MCPCatalogMonitorSummaryState] = {
    "disabled",
    "drift",
    "healthy",
    "unavailable",
}


def check_mcp_catalog_monitor_summary_state(value: str) -> MCPCatalogMonitorSummaryState:
    if value in MCP_CATALOG_MONITOR_SUMMARY_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_CATALOG_MONITOR_SUMMARY_STATE_VALUES!r}")
