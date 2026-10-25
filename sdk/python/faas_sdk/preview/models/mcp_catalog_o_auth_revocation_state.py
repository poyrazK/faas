from typing import Literal

MCPCatalogOAuthRevocationState = Literal["manual_required", "pending", "provider_acknowledged"]

MCP_CATALOG_O_AUTH_REVOCATION_STATE_VALUES: set[MCPCatalogOAuthRevocationState] = {
    "manual_required",
    "pending",
    "provider_acknowledged",
}


def check_mcp_catalog_o_auth_revocation_state(value: str) -> MCPCatalogOAuthRevocationState:
    if value in MCP_CATALOG_O_AUTH_REVOCATION_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_CATALOG_O_AUTH_REVOCATION_STATE_VALUES!r}")
