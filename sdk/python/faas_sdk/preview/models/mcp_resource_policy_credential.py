from typing import Literal

MCPResourcePolicyCredential = Literal["consumer_key"]

MCP_RESOURCE_POLICY_CREDENTIAL_VALUES: set[MCPResourcePolicyCredential] = {
    "consumer_key",
}


def check_mcp_resource_policy_credential(value: str) -> MCPResourcePolicyCredential:
    if value in MCP_RESOURCE_POLICY_CREDENTIAL_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_RESOURCE_POLICY_CREDENTIAL_VALUES!r}")
