from typing import Literal

MCPReadinessNotificationQueueState = Literal["delayed", "delivering", "idle", "maintenance", "paused", "ready"]

MCP_READINESS_NOTIFICATION_QUEUE_STATE_VALUES: set[MCPReadinessNotificationQueueState] = {
    "delayed",
    "delivering",
    "idle",
    "maintenance",
    "paused",
    "ready",
}


def check_mcp_readiness_notification_queue_state(value: str) -> MCPReadinessNotificationQueueState:
    if value in MCP_READINESS_NOTIFICATION_QUEUE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_READINESS_NOTIFICATION_QUEUE_STATE_VALUES!r}")
