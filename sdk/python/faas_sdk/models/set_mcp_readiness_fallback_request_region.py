from typing import Literal

SetMCPReadinessFallbackRequestRegion = Literal["eu", "us"]

SET_MCP_READINESS_FALLBACK_REQUEST_REGION_VALUES: set[SetMCPReadinessFallbackRequestRegion] = {
    "eu",
    "us",
}


def check_set_mcp_readiness_fallback_request_region(value: str) -> SetMCPReadinessFallbackRequestRegion:
    if value in SET_MCP_READINESS_FALLBACK_REQUEST_REGION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SET_MCP_READINESS_FALLBACK_REQUEST_REGION_VALUES!r}")
