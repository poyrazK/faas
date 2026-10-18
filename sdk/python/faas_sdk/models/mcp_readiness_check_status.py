from typing import Literal

MCPReadinessCheckStatus = Literal["failed", "passed"]

MCP_READINESS_CHECK_STATUS_VALUES: set[MCPReadinessCheckStatus] = {
    "failed",
    "passed",
}


def check_mcp_readiness_check_status(value: str) -> MCPReadinessCheckStatus:
    if value in MCP_READINESS_CHECK_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_READINESS_CHECK_STATUS_VALUES!r}")
