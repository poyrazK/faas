from typing import Literal

MCPReadinessDestinationDeliveryProvider = Literal["pagerduty", "webhook"]

MCP_READINESS_DESTINATION_DELIVERY_PROVIDER_VALUES: set[MCPReadinessDestinationDeliveryProvider] = {
    "pagerduty",
    "webhook",
}


def check_mcp_readiness_destination_delivery_provider(value: str) -> MCPReadinessDestinationDeliveryProvider:
    if value in MCP_READINESS_DESTINATION_DELIVERY_PROVIDER_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_READINESS_DESTINATION_DELIVERY_PROVIDER_VALUES!r}"
    )
