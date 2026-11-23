from typing import Literal

MCPReadinessNotificationQueueFailureKind = Literal["http_4xx", "http_5xx", "http_other", "lease_expired", "transport"]

MCP_READINESS_NOTIFICATION_QUEUE_FAILURE_KIND_VALUES: set[MCPReadinessNotificationQueueFailureKind] = {
    "http_4xx",
    "http_5xx",
    "http_other",
    "lease_expired",
    "transport",
}


def check_mcp_readiness_notification_queue_failure_kind(value: str) -> MCPReadinessNotificationQueueFailureKind:
    if value in MCP_READINESS_NOTIFICATION_QUEUE_FAILURE_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_READINESS_NOTIFICATION_QUEUE_FAILURE_KIND_VALUES!r}"
    )
