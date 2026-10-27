from typing import Literal

MCPCatalogOAuthRequestAction = Literal["begin", "check", "complete", "disconnect"]

MCP_CATALOG_O_AUTH_REQUEST_ACTION_VALUES: set[MCPCatalogOAuthRequestAction] = {
    "begin",
    "check",
    "complete",
    "disconnect",
}


def check_mcp_catalog_o_auth_request_action(value: str) -> MCPCatalogOAuthRequestAction:
    if value in MCP_CATALOG_O_AUTH_REQUEST_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_CATALOG_O_AUTH_REQUEST_ACTION_VALUES!r}")
