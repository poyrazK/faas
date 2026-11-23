from typing import Literal

MCPReadinessNotificationReceiptDestination = Literal["fallback", "primary"]

MCP_READINESS_NOTIFICATION_RECEIPT_DESTINATION_VALUES: set[MCPReadinessNotificationReceiptDestination] = {
    "fallback",
    "primary",
}


def check_mcp_readiness_notification_receipt_destination(value: str) -> MCPReadinessNotificationReceiptDestination:
    if value in MCP_READINESS_NOTIFICATION_RECEIPT_DESTINATION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_READINESS_NOTIFICATION_RECEIPT_DESTINATION_VALUES!r}"
    )
