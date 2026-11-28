from typing import Literal

MCPContractCheckVerdict = Literal["breaking", "compatible", "review", "unchanged", "unverified"]

MCP_CONTRACT_CHECK_VERDICT_VALUES: set[MCPContractCheckVerdict] = {
    "breaking",
    "compatible",
    "review",
    "unchanged",
    "unverified",
}


def check_mcp_contract_check_verdict(value: str) -> MCPContractCheckVerdict:
    if value in MCP_CONTRACT_CHECK_VERDICT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_CONTRACT_CHECK_VERDICT_VALUES!r}")
