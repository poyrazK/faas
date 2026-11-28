from typing import Literal

MCPCatalogObserverStatusState = Literal["checking", "degraded", "overdue", "paused", "scheduled", "stopped"]

MCP_CATALOG_OBSERVER_STATUS_STATE_VALUES: set[MCPCatalogObserverStatusState] = {
    "checking",
    "degraded",
    "overdue",
    "paused",
    "scheduled",
    "stopped",
}


def check_mcp_catalog_observer_status_state(value: str) -> MCPCatalogObserverStatusState:
    if value in MCP_CATALOG_OBSERVER_STATUS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_CATALOG_OBSERVER_STATUS_STATE_VALUES!r}")
