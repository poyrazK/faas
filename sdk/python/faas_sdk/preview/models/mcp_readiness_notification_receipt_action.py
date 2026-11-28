from typing import Literal

MCPReadinessNotificationReceiptAction = Literal["abandon", "retry"]

MCP_READINESS_NOTIFICATION_RECEIPT_ACTION_VALUES: set[MCPReadinessNotificationReceiptAction] = {
    "abandon",
    "retry",
}


def check_mcp_readiness_notification_receipt_action(value: str) -> MCPReadinessNotificationReceiptAction:
    if value in MCP_READINESS_NOTIFICATION_RECEIPT_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_READINESS_NOTIFICATION_RECEIPT_ACTION_VALUES!r}")
