from typing import Literal

MCPReadinessFallbackPolicyIntegrationKeyMasked = Literal["***"]

MCP_READINESS_FALLBACK_POLICY_INTEGRATION_KEY_MASKED_VALUES: set[MCPReadinessFallbackPolicyIntegrationKeyMasked] = {
    "***",
}


def check_mcp_readiness_fallback_policy_integration_key_masked(
    value: str,
) -> MCPReadinessFallbackPolicyIntegrationKeyMasked:
    if value in MCP_READINESS_FALLBACK_POLICY_INTEGRATION_KEY_MASKED_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_READINESS_FALLBACK_POLICY_INTEGRATION_KEY_MASKED_VALUES!r}"
    )
