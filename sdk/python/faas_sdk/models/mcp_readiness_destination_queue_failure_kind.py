from typing import Literal

MCPReadinessDestinationQueueFailureKind = Literal["http_4xx", "http_5xx", "http_other", "lease_expired", "transport"]

MCP_READINESS_DESTINATION_QUEUE_FAILURE_KIND_VALUES: set[MCPReadinessDestinationQueueFailureKind] = {
    "http_4xx",
    "http_5xx",
    "http_other",
    "lease_expired",
    "transport",
}


def check_mcp_readiness_destination_queue_failure_kind(value: str) -> MCPReadinessDestinationQueueFailureKind:
    if value in MCP_READINESS_DESTINATION_QUEUE_FAILURE_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_READINESS_DESTINATION_QUEUE_FAILURE_KIND_VALUES!r}"
    )
