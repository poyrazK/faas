from typing import Literal

SetMCPReadinessPagerDutyAcknowledgementsRequestDestination = Literal["fallback", "primary"]

SET_MCP_READINESS_PAGER_DUTY_ACKNOWLEDGEMENTS_REQUEST_DESTINATION_VALUES: set[
    SetMCPReadinessPagerDutyAcknowledgementsRequestDestination
] = {
    "fallback",
    "primary",
}


def check_set_mcp_readiness_pager_duty_acknowledgements_request_destination(
    value: str,
) -> SetMCPReadinessPagerDutyAcknowledgementsRequestDestination:
    if value in SET_MCP_READINESS_PAGER_DUTY_ACKNOWLEDGEMENTS_REQUEST_DESTINATION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SET_MCP_READINESS_PAGER_DUTY_ACKNOWLEDGEMENTS_REQUEST_DESTINATION_VALUES!r}"
    )
