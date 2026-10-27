from typing import Literal

MCPCatalogObserverOAuthConfigProfile = Literal["generic", "google", "oidc"]

MCP_CATALOG_OBSERVER_O_AUTH_CONFIG_PROFILE_VALUES: set[MCPCatalogObserverOAuthConfigProfile] = {
    "generic",
    "google",
    "oidc",
}


def check_mcp_catalog_observer_o_auth_config_profile(value: str) -> MCPCatalogObserverOAuthConfigProfile:
    if value in MCP_CATALOG_OBSERVER_O_AUTH_CONFIG_PROFILE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_CATALOG_OBSERVER_O_AUTH_CONFIG_PROFILE_VALUES!r}"
    )
