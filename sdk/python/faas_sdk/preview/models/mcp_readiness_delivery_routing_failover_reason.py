from typing import Literal

MCPReadinessDeliveryRoutingFailoverReason = Literal[
    "incident_sticky_fallback", "primary_failure_threshold", "primary_queue_blocked", "recovery_to_primary"
]

MCP_READINESS_DELIVERY_ROUTING_FAILOVER_REASON_VALUES: set[MCPReadinessDeliveryRoutingFailoverReason] = {
    "incident_sticky_fallback",
    "primary_failure_threshold",
    "primary_queue_blocked",
    "recovery_to_primary",
}


def check_mcp_readiness_delivery_routing_failover_reason(value: str) -> MCPReadinessDeliveryRoutingFailoverReason:
    if value in MCP_READINESS_DELIVERY_ROUTING_FAILOVER_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_READINESS_DELIVERY_ROUTING_FAILOVER_REASON_VALUES!r}"
    )
