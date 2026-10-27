from typing import Literal

MCPCatalogOAuthRenewalOutcome = Literal["issuance_unknown", "response_recorded"]

MCP_CATALOG_O_AUTH_RENEWAL_OUTCOME_VALUES: set[MCPCatalogOAuthRenewalOutcome] = {
    "issuance_unknown",
    "response_recorded",
}


def check_mcp_catalog_o_auth_renewal_outcome(value: str) -> MCPCatalogOAuthRenewalOutcome:
    if value in MCP_CATALOG_O_AUTH_RENEWAL_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_CATALOG_O_AUTH_RENEWAL_OUTCOME_VALUES!r}")
