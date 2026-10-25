from typing import Literal

MCPCatalogOAuthLifecycleLocalState = Literal["connected", "disconnected", "reauthorization_required"]

MCP_CATALOG_O_AUTH_LIFECYCLE_LOCAL_STATE_VALUES: set[MCPCatalogOAuthLifecycleLocalState] = {
    "connected",
    "disconnected",
    "reauthorization_required",
}


def check_mcp_catalog_o_auth_lifecycle_local_state(value: str) -> MCPCatalogOAuthLifecycleLocalState:
    if value in MCP_CATALOG_O_AUTH_LIFECYCLE_LOCAL_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_CATALOG_O_AUTH_LIFECYCLE_LOCAL_STATE_VALUES!r}")
