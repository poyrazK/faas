from typing import Literal

MCPReadinessDestinationQueueDestination = Literal["fallback", "primary"]

MCP_READINESS_DESTINATION_QUEUE_DESTINATION_VALUES: set[MCPReadinessDestinationQueueDestination] = {
    "fallback",
    "primary",
}


def check_mcp_readiness_destination_queue_destination(value: str) -> MCPReadinessDestinationQueueDestination:
    if value in MCP_READINESS_DESTINATION_QUEUE_DESTINATION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_READINESS_DESTINATION_QUEUE_DESTINATION_VALUES!r}"
    )
