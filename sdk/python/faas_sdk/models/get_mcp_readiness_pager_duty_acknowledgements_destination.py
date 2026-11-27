from typing import Literal

GetMCPReadinessPagerDutyAcknowledgementsDestination = Literal["fallback", "primary"]

GET_MCP_READINESS_PAGER_DUTY_ACKNOWLEDGEMENTS_DESTINATION_VALUES: set[
    GetMCPReadinessPagerDutyAcknowledgementsDestination
] = {
    "fallback",
    "primary",
}


def check_get_mcp_readiness_pager_duty_acknowledgements_destination(
    value: str,
) -> GetMCPReadinessPagerDutyAcknowledgementsDestination:
    if value in GET_MCP_READINESS_PAGER_DUTY_ACKNOWLEDGEMENTS_DESTINATION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {GET_MCP_READINESS_PAGER_DUTY_ACKNOWLEDGEMENTS_DESTINATION_VALUES!r}"
    )
