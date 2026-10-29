from typing import Literal

MCPReadinessDestinationQueueProvider = Literal["pagerduty", "webhook"]

MCP_READINESS_DESTINATION_QUEUE_PROVIDER_VALUES: set[MCPReadinessDestinationQueueProvider] = {
    "pagerduty",
    "webhook",
}


def check_mcp_readiness_destination_queue_provider(value: str) -> MCPReadinessDestinationQueueProvider:
    if value in MCP_READINESS_DESTINATION_QUEUE_PROVIDER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_READINESS_DESTINATION_QUEUE_PROVIDER_VALUES!r}")
