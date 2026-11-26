from typing import Literal

SetMCPReadinessPagerDutyRequestRegion = Literal["eu", "us"]

SET_MCP_READINESS_PAGER_DUTY_REQUEST_REGION_VALUES: set[SetMCPReadinessPagerDutyRequestRegion] = {
    "eu",
    "us",
}


def check_set_mcp_readiness_pager_duty_request_region(value: str) -> SetMCPReadinessPagerDutyRequestRegion:
    if value in SET_MCP_READINESS_PAGER_DUTY_REQUEST_REGION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SET_MCP_READINESS_PAGER_DUTY_REQUEST_REGION_VALUES!r}"
    )
