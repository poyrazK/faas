from typing import Literal

MCPCatalogOAuthLifecycleCredentialState = Literal["credential_changed", "reconnect_required", "retirement_held"]

MCP_CATALOG_O_AUTH_LIFECYCLE_CREDENTIAL_STATE_VALUES: set[MCPCatalogOAuthLifecycleCredentialState] = {
    "credential_changed",
    "reconnect_required",
    "retirement_held",
}


def check_mcp_catalog_o_auth_lifecycle_credential_state(value: str) -> MCPCatalogOAuthLifecycleCredentialState:
    if value in MCP_CATALOG_O_AUTH_LIFECYCLE_CREDENTIAL_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_CATALOG_O_AUTH_LIFECYCLE_CREDENTIAL_STATE_VALUES!r}"
    )
