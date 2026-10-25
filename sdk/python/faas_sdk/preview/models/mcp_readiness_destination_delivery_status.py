from typing import Literal

MCPReadinessDestinationDeliveryStatus = Literal[
    "abandoned", "delivered", "delivering", "failed", "pending", "superseded", "suppressed"
]

MCP_READINESS_DESTINATION_DELIVERY_STATUS_VALUES: set[MCPReadinessDestinationDeliveryStatus] = {
    "abandoned",
    "delivered",
    "delivering",
    "failed",
    "pending",
    "superseded",
    "suppressed",
}


def check_mcp_readiness_destination_delivery_status(value: str) -> MCPReadinessDestinationDeliveryStatus:
    if value in MCP_READINESS_DESTINATION_DELIVERY_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_READINESS_DESTINATION_DELIVERY_STATUS_VALUES!r}")
