from typing import Literal

MCPCatalogOAuthResponseState = Literal[
    "authorization_pending", "compatible", "connected", "disconnected", "unavailable", "unsupported", "unverified"
]

MCP_CATALOG_O_AUTH_RESPONSE_STATE_VALUES: set[MCPCatalogOAuthResponseState] = {
    "authorization_pending",
    "compatible",
    "connected",
    "disconnected",
    "unavailable",
    "unsupported",
    "unverified",
}


def check_mcp_catalog_o_auth_response_state(value: str) -> MCPCatalogOAuthResponseState:
    if value in MCP_CATALOG_O_AUTH_RESPONSE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_CATALOG_O_AUTH_RESPONSE_STATE_VALUES!r}")
