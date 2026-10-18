from typing import Literal

MCPConnectionHealthIssueKind = Literal[
    "issuance_unknown", "reauthorization_required", "renewal_failed", "retirement_expiring"
]

MCP_CONNECTION_HEALTH_ISSUE_KIND_VALUES: set[MCPConnectionHealthIssueKind] = {
    "issuance_unknown",
    "reauthorization_required",
    "renewal_failed",
    "retirement_expiring",
}


def check_mcp_connection_health_issue_kind(value: str) -> MCPConnectionHealthIssueKind:
    if value in MCP_CONNECTION_HEALTH_ISSUE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_CONNECTION_HEALTH_ISSUE_KIND_VALUES!r}")
