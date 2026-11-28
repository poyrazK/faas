from typing import Literal

MCPCatalogOAuthRevocationFailureReason = Literal["credential_unavailable", "provider_unavailable"]

MCP_CATALOG_O_AUTH_REVOCATION_FAILURE_REASON_VALUES: set[MCPCatalogOAuthRevocationFailureReason] = {
    "credential_unavailable",
    "provider_unavailable",
}


def check_mcp_catalog_o_auth_revocation_failure_reason(value: str) -> MCPCatalogOAuthRevocationFailureReason:
    if value in MCP_CATALOG_O_AUTH_REVOCATION_FAILURE_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_CATALOG_O_AUTH_REVOCATION_FAILURE_REASON_VALUES!r}"
    )
