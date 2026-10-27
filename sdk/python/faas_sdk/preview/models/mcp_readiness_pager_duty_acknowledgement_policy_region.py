from typing import Literal

MCPReadinessPagerDutyAcknowledgementPolicyRegion = Literal["eu", "us"]

MCP_READINESS_PAGER_DUTY_ACKNOWLEDGEMENT_POLICY_REGION_VALUES: set[MCPReadinessPagerDutyAcknowledgementPolicyRegion] = {
    "eu",
    "us",
}


def check_mcp_readiness_pager_duty_acknowledgement_policy_region(
    value: str,
) -> MCPReadinessPagerDutyAcknowledgementPolicyRegion:
    if value in MCP_READINESS_PAGER_DUTY_ACKNOWLEDGEMENT_POLICY_REGION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_READINESS_PAGER_DUTY_ACKNOWLEDGEMENT_POLICY_REGION_VALUES!r}"
    )
