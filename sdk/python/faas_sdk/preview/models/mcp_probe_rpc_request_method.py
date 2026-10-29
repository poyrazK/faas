from typing import Literal

MCPProbeRPCRequestMethod = Literal[
    "initialize",
    "notifications/initialized",
    "prompts/list",
    "resources/list",
    "resources/templates/list",
    "server/discover",
    "tools/list",
]

MCP_PROBE_RPC_REQUEST_METHOD_VALUES: set[MCPProbeRPCRequestMethod] = {
    "initialize",
    "notifications/initialized",
    "prompts/list",
    "resources/list",
    "resources/templates/list",
    "server/discover",
    "tools/list",
}


def check_mcp_probe_rpc_request_method(value: str) -> MCPProbeRPCRequestMethod:
    if value in MCP_PROBE_RPC_REQUEST_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_PROBE_RPC_REQUEST_METHOD_VALUES!r}")
