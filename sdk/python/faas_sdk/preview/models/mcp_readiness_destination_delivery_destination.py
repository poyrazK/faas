from typing import Literal

MCPReadinessDestinationDeliveryDestination = Literal["fallback", "primary"]

MCP_READINESS_DESTINATION_DELIVERY_DESTINATION_VALUES: set[MCPReadinessDestinationDeliveryDestination] = {
    "fallback",
    "primary",
}


def check_mcp_readiness_destination_delivery_destination(value: str) -> MCPReadinessDestinationDeliveryDestination:
    if value in MCP_READINESS_DESTINATION_DELIVERY_DESTINATION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_READINESS_DESTINATION_DELIVERY_DESTINATION_VALUES!r}"
    )
