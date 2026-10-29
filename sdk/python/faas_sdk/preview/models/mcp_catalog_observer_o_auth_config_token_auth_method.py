from typing import Literal

MCPCatalogObserverOAuthConfigTokenAuthMethod = Literal["client_secret_basic", "client_secret_post", "none"]

MCP_CATALOG_OBSERVER_O_AUTH_CONFIG_TOKEN_AUTH_METHOD_VALUES: set[MCPCatalogObserverOAuthConfigTokenAuthMethod] = {
    "client_secret_basic",
    "client_secret_post",
    "none",
}


def check_mcp_catalog_observer_o_auth_config_token_auth_method(
    value: str,
) -> MCPCatalogObserverOAuthConfigTokenAuthMethod:
    if value in MCP_CATALOG_OBSERVER_O_AUTH_CONFIG_TOKEN_AUTH_METHOD_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_CATALOG_OBSERVER_O_AUTH_CONFIG_TOKEN_AUTH_METHOD_VALUES!r}"
    )
