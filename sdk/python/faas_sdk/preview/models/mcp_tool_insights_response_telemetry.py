from typing import Literal

MCPToolInsightsResponseTelemetry = Literal["not_observed", "reporting"]

MCP_TOOL_INSIGHTS_RESPONSE_TELEMETRY_VALUES: set[MCPToolInsightsResponseTelemetry] = {
    "not_observed",
    "reporting",
}


def check_mcp_tool_insights_response_telemetry(value: str) -> MCPToolInsightsResponseTelemetry:
    if value in MCP_TOOL_INSIGHTS_RESPONSE_TELEMETRY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_TOOL_INSIGHTS_RESPONSE_TELEMETRY_VALUES!r}")
