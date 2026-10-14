from typing import Literal

MCPCatalogOAuthCheckState = Literal["failed", "passed", "unverified"]

MCP_CATALOG_O_AUTH_CHECK_STATE_VALUES: set[MCPCatalogOAuthCheckState] = {
    "failed",
    "passed",
    "unverified",
}


def check_mcp_catalog_o_auth_check_state(value: str) -> MCPCatalogOAuthCheckState:
    if value in MCP_CATALOG_O_AUTH_CHECK_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_CATALOG_O_AUTH_CHECK_STATE_VALUES!r}")
