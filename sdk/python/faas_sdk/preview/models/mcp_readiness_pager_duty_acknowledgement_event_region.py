from typing import Literal

MCPReadinessPagerDutyAcknowledgementEventRegion = Literal["eu", "us"]

MCP_READINESS_PAGER_DUTY_ACKNOWLEDGEMENT_EVENT_REGION_VALUES: set[MCPReadinessPagerDutyAcknowledgementEventRegion] = {
    "eu",
    "us",
}


def check_mcp_readiness_pager_duty_acknowledgement_event_region(
    value: str,
) -> MCPReadinessPagerDutyAcknowledgementEventRegion:
    if value in MCP_READINESS_PAGER_DUTY_ACKNOWLEDGEMENT_EVENT_REGION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_READINESS_PAGER_DUTY_ACKNOWLEDGEMENT_EVENT_REGION_VALUES!r}"
    )
