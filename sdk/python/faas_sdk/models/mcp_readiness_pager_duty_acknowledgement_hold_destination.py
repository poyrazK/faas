from typing import Literal

MCPReadinessPagerDutyAcknowledgementHoldDestination = Literal["fallback", "primary"]

MCP_READINESS_PAGER_DUTY_ACKNOWLEDGEMENT_HOLD_DESTINATION_VALUES: set[
    MCPReadinessPagerDutyAcknowledgementHoldDestination
] = {
    "fallback",
    "primary",
}


def check_mcp_readiness_pager_duty_acknowledgement_hold_destination(
    value: str,
) -> MCPReadinessPagerDutyAcknowledgementHoldDestination:
    if value in MCP_READINESS_PAGER_DUTY_ACKNOWLEDGEMENT_HOLD_DESTINATION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_READINESS_PAGER_DUTY_ACKNOWLEDGEMENT_HOLD_DESTINATION_VALUES!r}"
    )
