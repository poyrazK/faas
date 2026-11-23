from typing import Literal

MCPReadinessAttentionReceiptSource = Literal["pagerduty"]

MCP_READINESS_ATTENTION_RECEIPT_SOURCE_VALUES: set[MCPReadinessAttentionReceiptSource] = {
    "pagerduty",
}


def check_mcp_readiness_attention_receipt_source(value: str) -> MCPReadinessAttentionReceiptSource:
    if value in MCP_READINESS_ATTENTION_RECEIPT_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_READINESS_ATTENTION_RECEIPT_SOURCE_VALUES!r}")
