from typing import Literal

MCPReadinessAcknowledgementSource = Literal["pagerduty"]

MCP_READINESS_ACKNOWLEDGEMENT_SOURCE_VALUES: set[MCPReadinessAcknowledgementSource] = {
    "pagerduty",
}


def check_mcp_readiness_acknowledgement_source(value: str) -> MCPReadinessAcknowledgementSource:
    if value in MCP_READINESS_ACKNOWLEDGEMENT_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_READINESS_ACKNOWLEDGEMENT_SOURCE_VALUES!r}")
