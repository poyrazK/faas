from typing import Literal

MCPCatalogOAuthResponseProviderRevocation = Literal[
    "manual_required", "not_required", "pending", "provider_acknowledged"
]

MCP_CATALOG_O_AUTH_RESPONSE_PROVIDER_REVOCATION_VALUES: set[MCPCatalogOAuthResponseProviderRevocation] = {
    "manual_required",
    "not_required",
    "pending",
    "provider_acknowledged",
}


def check_mcp_catalog_o_auth_response_provider_revocation(value: str) -> MCPCatalogOAuthResponseProviderRevocation:
    if value in MCP_CATALOG_O_AUTH_RESPONSE_PROVIDER_REVOCATION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_CATALOG_O_AUTH_RESPONSE_PROVIDER_REVOCATION_VALUES!r}"
    )
