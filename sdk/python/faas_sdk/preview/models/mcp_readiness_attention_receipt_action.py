from typing import Literal

MCPReadinessAttentionReceiptAction = Literal["acknowledge", "configure_reminders", "release"]

MCP_READINESS_ATTENTION_RECEIPT_ACTION_VALUES: set[MCPReadinessAttentionReceiptAction] = {
    "acknowledge",
    "configure_reminders",
    "release",
}


def check_mcp_readiness_attention_receipt_action(value: str) -> MCPReadinessAttentionReceiptAction:
    if value in MCP_READINESS_ATTENTION_RECEIPT_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_READINESS_ATTENTION_RECEIPT_ACTION_VALUES!r}")
