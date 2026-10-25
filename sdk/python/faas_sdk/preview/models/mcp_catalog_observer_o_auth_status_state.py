from typing import Literal

MCPCatalogObserverOAuthStatusState = Literal["connected", "reauthorization_required"]

MCP_CATALOG_OBSERVER_O_AUTH_STATUS_STATE_VALUES: set[MCPCatalogObserverOAuthStatusState] = {
    "connected",
    "reauthorization_required",
}


def check_mcp_catalog_observer_o_auth_status_state(value: str) -> MCPCatalogObserverOAuthStatusState:
    if value in MCP_CATALOG_OBSERVER_O_AUTH_STATUS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_CATALOG_OBSERVER_O_AUTH_STATUS_STATE_VALUES!r}")
