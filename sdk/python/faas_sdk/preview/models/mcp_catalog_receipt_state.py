from typing import Literal

MCPCatalogReceiptState = Literal["drift", "healthy", "unavailable"]

MCP_CATALOG_RECEIPT_STATE_VALUES: set[MCPCatalogReceiptState] = {
    "drift",
    "healthy",
    "unavailable",
}


def check_mcp_catalog_receipt_state(value: str) -> MCPCatalogReceiptState:
    if value in MCP_CATALOG_RECEIPT_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_CATALOG_RECEIPT_STATE_VALUES!r}")
