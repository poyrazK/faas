from typing import Literal

MCPReadinessDeliveryRoutingLastDestination = Literal["fallback", "primary"]

MCP_READINESS_DELIVERY_ROUTING_LAST_DESTINATION_VALUES: set[MCPReadinessDeliveryRoutingLastDestination] = {
    "fallback",
    "primary",
}


def check_mcp_readiness_delivery_routing_last_destination(value: str) -> MCPReadinessDeliveryRoutingLastDestination:
    if value in MCP_READINESS_DELIVERY_ROUTING_LAST_DESTINATION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_READINESS_DELIVERY_ROUTING_LAST_DESTINATION_VALUES!r}"
    )
