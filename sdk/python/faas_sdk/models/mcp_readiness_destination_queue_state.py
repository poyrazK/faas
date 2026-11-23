from typing import Literal

MCPReadinessDestinationQueueState = Literal[
    "delayed", "delivering", "idle", "maintenance", "paused", "ready", "waiting_primary", "waiting_receiver"
]

MCP_READINESS_DESTINATION_QUEUE_STATE_VALUES: set[MCPReadinessDestinationQueueState] = {
    "delayed",
    "delivering",
    "idle",
    "maintenance",
    "paused",
    "ready",
    "waiting_primary",
    "waiting_receiver",
}


def check_mcp_readiness_destination_queue_state(value: str) -> MCPReadinessDestinationQueueState:
    if value in MCP_READINESS_DESTINATION_QUEUE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_READINESS_DESTINATION_QUEUE_STATE_VALUES!r}")
