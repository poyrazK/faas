from typing import Literal

MCPReadinessPagerDutyAcknowledgementPolicyDestination = Literal["fallback", "primary"]

MCP_READINESS_PAGER_DUTY_ACKNOWLEDGEMENT_POLICY_DESTINATION_VALUES: set[
    MCPReadinessPagerDutyAcknowledgementPolicyDestination
] = {
    "fallback",
    "primary",
}


def check_mcp_readiness_pager_duty_acknowledgement_policy_destination(
    value: str,
) -> MCPReadinessPagerDutyAcknowledgementPolicyDestination:
    if value in MCP_READINESS_PAGER_DUTY_ACKNOWLEDGEMENT_POLICY_DESTINATION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_READINESS_PAGER_DUTY_ACKNOWLEDGEMENT_POLICY_DESTINATION_VALUES!r}"
    )
