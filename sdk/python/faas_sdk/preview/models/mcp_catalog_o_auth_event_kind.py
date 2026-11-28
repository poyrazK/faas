from typing import Literal

MCPCatalogOAuthEventKind = Literal[
    "connected",
    "credentials_discarded",
    "disconnected",
    "reauthorization_required",
    "renewed",
    "revocation_acknowledged",
    "revocation_manual_required",
]

MCP_CATALOG_O_AUTH_EVENT_KIND_VALUES: set[MCPCatalogOAuthEventKind] = {
    "connected",
    "credentials_discarded",
    "disconnected",
    "reauthorization_required",
    "renewed",
    "revocation_acknowledged",
    "revocation_manual_required",
}


def check_mcp_catalog_o_auth_event_kind(value: str) -> MCPCatalogOAuthEventKind:
    if value in MCP_CATALOG_O_AUTH_EVENT_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_CATALOG_O_AUTH_EVENT_KIND_VALUES!r}")
