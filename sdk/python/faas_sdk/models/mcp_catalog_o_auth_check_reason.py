from typing import Literal

MCPCatalogOAuthCheckReason = Literal[
    "capability_not_advertised",
    "metadata_not_configured",
    "metadata_unavailable",
    "required_capability_not_supported",
    "reviewed_endpoint_mismatch",
    "scope_not_advertised",
]

MCP_CATALOG_O_AUTH_CHECK_REASON_VALUES: set[MCPCatalogOAuthCheckReason] = {
    "capability_not_advertised",
    "metadata_not_configured",
    "metadata_unavailable",
    "required_capability_not_supported",
    "reviewed_endpoint_mismatch",
    "scope_not_advertised",
}


def check_mcp_catalog_o_auth_check_reason(value: str) -> MCPCatalogOAuthCheckReason:
    if value in MCP_CATALOG_O_AUTH_CHECK_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_CATALOG_O_AUTH_CHECK_REASON_VALUES!r}")
