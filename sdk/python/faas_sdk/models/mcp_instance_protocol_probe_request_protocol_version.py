from typing import Literal

MCPInstanceProtocolProbeRequestProtocolVersion = Literal["2025-11-25", "2026-07-28"]

MCP_INSTANCE_PROTOCOL_PROBE_REQUEST_PROTOCOL_VERSION_VALUES: set[MCPInstanceProtocolProbeRequestProtocolVersion] = {
    "2025-11-25",
    "2026-07-28",
}


def check_mcp_instance_protocol_probe_request_protocol_version(
    value: str,
) -> MCPInstanceProtocolProbeRequestProtocolVersion:
    if value in MCP_INSTANCE_PROTOCOL_PROBE_REQUEST_PROTOCOL_VERSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_INSTANCE_PROTOCOL_PROBE_REQUEST_PROTOCOL_VERSION_VALUES!r}"
    )
