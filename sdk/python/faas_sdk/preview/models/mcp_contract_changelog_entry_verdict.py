from typing import Literal

MCPContractChangelogEntryVerdict = Literal["breaking", "compatible", "review", "unverified"]

MCP_CONTRACT_CHANGELOG_ENTRY_VERDICT_VALUES: set[MCPContractChangelogEntryVerdict] = {
    "breaking",
    "compatible",
    "review",
    "unverified",
}


def check_mcp_contract_changelog_entry_verdict(value: str) -> MCPContractChangelogEntryVerdict:
    if value in MCP_CONTRACT_CHANGELOG_ENTRY_VERDICT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_CONTRACT_CHANGELOG_ENTRY_VERDICT_VALUES!r}")
