from typing import Literal

MCPReadinessPagerDutyAcknowledgementHoldRegion = Literal["eu", "us"]

MCP_READINESS_PAGER_DUTY_ACKNOWLEDGEMENT_HOLD_REGION_VALUES: set[MCPReadinessPagerDutyAcknowledgementHoldRegion] = {
    "eu",
    "us",
}


def check_mcp_readiness_pager_duty_acknowledgement_hold_region(
    value: str,
) -> MCPReadinessPagerDutyAcknowledgementHoldRegion:
    if value in MCP_READINESS_PAGER_DUTY_ACKNOWLEDGEMENT_HOLD_REGION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_READINESS_PAGER_DUTY_ACKNOWLEDGEMENT_HOLD_REGION_VALUES!r}"
    )
