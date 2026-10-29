from typing import Literal

MCPCatalogObserverStatusFailureReason = Literal[
    "comparison_unavailable", "credential_unavailable", "source_unavailable"
]

MCP_CATALOG_OBSERVER_STATUS_FAILURE_REASON_VALUES: set[MCPCatalogObserverStatusFailureReason] = {
    "comparison_unavailable",
    "credential_unavailable",
    "source_unavailable",
}


def check_mcp_catalog_observer_status_failure_reason(value: str) -> MCPCatalogObserverStatusFailureReason:
    if value in MCP_CATALOG_OBSERVER_STATUS_FAILURE_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_CATALOG_OBSERVER_STATUS_FAILURE_REASON_VALUES!r}"
    )
