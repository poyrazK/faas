from typing import Literal

MCPReadinessPagerDutyPolicyIntegrationKeyMasked = Literal["***"]

MCP_READINESS_PAGER_DUTY_POLICY_INTEGRATION_KEY_MASKED_VALUES: set[MCPReadinessPagerDutyPolicyIntegrationKeyMasked] = {
    "***",
}


def check_mcp_readiness_pager_duty_policy_integration_key_masked(
    value: str,
) -> MCPReadinessPagerDutyPolicyIntegrationKeyMasked:
    if value in MCP_READINESS_PAGER_DUTY_POLICY_INTEGRATION_KEY_MASKED_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_READINESS_PAGER_DUTY_POLICY_INTEGRATION_KEY_MASKED_VALUES!r}"
    )
