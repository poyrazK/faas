from typing import Literal

MCPContractChangeSeverity = Literal["breaking", "informational", "needs_review"]

MCP_CONTRACT_CHANGE_SEVERITY_VALUES: set[MCPContractChangeSeverity] = {
    "breaking",
    "informational",
    "needs_review",
}


def check_mcp_contract_change_severity(value: str) -> MCPContractChangeSeverity:
    if value in MCP_CONTRACT_CHANGE_SEVERITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_CONTRACT_CHANGE_SEVERITY_VALUES!r}")
