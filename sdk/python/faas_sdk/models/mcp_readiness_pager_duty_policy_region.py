from typing import Literal

MCPReadinessPagerDutyPolicyRegion = Literal["eu", "us"]

MCP_READINESS_PAGER_DUTY_POLICY_REGION_VALUES: set[MCPReadinessPagerDutyPolicyRegion] = {
    "eu",
    "us",
}


def check_mcp_readiness_pager_duty_policy_region(value: str) -> MCPReadinessPagerDutyPolicyRegion:
    if value in MCP_READINESS_PAGER_DUTY_POLICY_REGION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_READINESS_PAGER_DUTY_POLICY_REGION_VALUES!r}")
