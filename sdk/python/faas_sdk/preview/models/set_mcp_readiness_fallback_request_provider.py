from typing import Literal

SetMCPReadinessFallbackRequestProvider = Literal["pagerduty", "webhook"]

SET_MCP_READINESS_FALLBACK_REQUEST_PROVIDER_VALUES: set[SetMCPReadinessFallbackRequestProvider] = {
    "pagerduty",
    "webhook",
}


def check_set_mcp_readiness_fallback_request_provider(value: str) -> SetMCPReadinessFallbackRequestProvider:
    if value in SET_MCP_READINESS_FALLBACK_REQUEST_PROVIDER_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SET_MCP_READINESS_FALLBACK_REQUEST_PROVIDER_VALUES!r}"
    )
