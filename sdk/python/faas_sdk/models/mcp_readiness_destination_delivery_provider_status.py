from typing import Literal

MCPReadinessDestinationDeliveryProviderStatus = Literal[
    "abandoned", "accepted", "delivering", "pending", "retrying", "superseded", "suppressed"
]

MCP_READINESS_DESTINATION_DELIVERY_PROVIDER_STATUS_VALUES: set[MCPReadinessDestinationDeliveryProviderStatus] = {
    "abandoned",
    "accepted",
    "delivering",
    "pending",
    "retrying",
    "superseded",
    "suppressed",
}


def check_mcp_readiness_destination_delivery_provider_status(
    value: str,
) -> MCPReadinessDestinationDeliveryProviderStatus:
    if value in MCP_READINESS_DESTINATION_DELIVERY_PROVIDER_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_READINESS_DESTINATION_DELIVERY_PROVIDER_STATUS_VALUES!r}"
    )
