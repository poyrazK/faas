from typing import Literal

MCPReadinessFallbackPolicyRegion = Literal["eu", "us"]

MCP_READINESS_FALLBACK_POLICY_REGION_VALUES: set[MCPReadinessFallbackPolicyRegion] = {
    "eu",
    "us",
}


def check_mcp_readiness_fallback_policy_region(value: str) -> MCPReadinessFallbackPolicyRegion:
    if value in MCP_READINESS_FALLBACK_POLICY_REGION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_READINESS_FALLBACK_POLICY_REGION_VALUES!r}")
