from typing import Literal

MCPReadinessRoleCatalogFormat = Literal[1, 2]

MCP_READINESS_ROLE_CATALOG_FORMAT_VALUES: set[MCPReadinessRoleCatalogFormat] = {
    1,
    2,
}


def check_mcp_readiness_role_catalog_format(value: int) -> MCPReadinessRoleCatalogFormat:
    if value in MCP_READINESS_ROLE_CATALOG_FORMAT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_READINESS_ROLE_CATALOG_FORMAT_VALUES!r}")
