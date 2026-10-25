from typing import Literal

MCPReadinessPagerDutyAcknowledgementEventDestination = Literal["fallback", "primary"]

MCP_READINESS_PAGER_DUTY_ACKNOWLEDGEMENT_EVENT_DESTINATION_VALUES: set[
    MCPReadinessPagerDutyAcknowledgementEventDestination
] = {
    "fallback",
    "primary",
}


def check_mcp_readiness_pager_duty_acknowledgement_event_destination(
    value: str,
) -> MCPReadinessPagerDutyAcknowledgementEventDestination:
    if value in MCP_READINESS_PAGER_DUTY_ACKNOWLEDGEMENT_EVENT_DESTINATION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_READINESS_PAGER_DUTY_ACKNOWLEDGEMENT_EVENT_DESTINATION_VALUES!r}"
    )
