from typing import Literal

MCPReadinessFallbackPolicyProvider = Literal["pagerduty", "webhook"]

MCP_READINESS_FALLBACK_POLICY_PROVIDER_VALUES: set[MCPReadinessFallbackPolicyProvider] = {
    "pagerduty",
    "webhook",
}


def check_mcp_readiness_fallback_policy_provider(value: str) -> MCPReadinessFallbackPolicyProvider:
    if value in MCP_READINESS_FALLBACK_POLICY_PROVIDER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_READINESS_FALLBACK_POLICY_PROVIDER_VALUES!r}")
