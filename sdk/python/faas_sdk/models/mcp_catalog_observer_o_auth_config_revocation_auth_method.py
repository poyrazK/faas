from typing import Literal

MCPCatalogObserverOAuthConfigRevocationAuthMethod = Literal["client_secret_basic", "client_secret_post", "none"]

MCP_CATALOG_OBSERVER_O_AUTH_CONFIG_REVOCATION_AUTH_METHOD_VALUES: set[
    MCPCatalogObserverOAuthConfigRevocationAuthMethod
] = {
    "client_secret_basic",
    "client_secret_post",
    "none",
}


def check_mcp_catalog_observer_o_auth_config_revocation_auth_method(
    value: str,
) -> MCPCatalogObserverOAuthConfigRevocationAuthMethod:
    if value in MCP_CATALOG_OBSERVER_O_AUTH_CONFIG_REVOCATION_AUTH_METHOD_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_CATALOG_OBSERVER_O_AUTH_CONFIG_REVOCATION_AUTH_METHOD_VALUES!r}"
    )
