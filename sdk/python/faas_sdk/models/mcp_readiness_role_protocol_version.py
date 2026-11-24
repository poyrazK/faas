from typing import Literal

MCPReadinessRoleProtocolVersion = Literal["2025-11-25", "2026-07-28"]

MCP_READINESS_ROLE_PROTOCOL_VERSION_VALUES: set[MCPReadinessRoleProtocolVersion] = {
    "2025-11-25",
    "2026-07-28",
}


def check_mcp_readiness_role_protocol_version(value: str) -> MCPReadinessRoleProtocolVersion:
    if value in MCP_READINESS_ROLE_PROTOCOL_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_READINESS_ROLE_PROTOCOL_VERSION_VALUES!r}")
