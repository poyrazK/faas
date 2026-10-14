from typing import Literal

MCPCatalogOAuthRenewalState = Literal["failed", "installed", "pending", "reauthorization_required", "saved"]

MCP_CATALOG_O_AUTH_RENEWAL_STATE_VALUES: set[MCPCatalogOAuthRenewalState] = {
    "failed",
    "installed",
    "pending",
    "reauthorization_required",
    "saved",
}


def check_mcp_catalog_o_auth_renewal_state(value: str) -> MCPCatalogOAuthRenewalState:
    if value in MCP_CATALOG_O_AUTH_RENEWAL_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_CATALOG_O_AUTH_RENEWAL_STATE_VALUES!r}")
