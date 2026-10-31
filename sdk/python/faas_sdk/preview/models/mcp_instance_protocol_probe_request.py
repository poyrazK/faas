from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_instance_protocol_probe_request_protocol_version import (
    MCPInstanceProtocolProbeRequestProtocolVersion,
    check_mcp_instance_protocol_probe_request_protocol_version,
)

if TYPE_CHECKING:
    from ..models.mcp_probe_rpc_request import MCPProbeRPCRequest


T = TypeVar("T", bound="MCPInstanceProtocolProbeRequest")


@_attrs_define
class MCPInstanceProtocolProbeRequest:
    """One bounded, read-only MCP JSON-RPC request to an exact replica."""

    host: str
    """Deployment preview hostname and optional port."""
    endpoint: str
    """Literal absolute MCP endpoint path without query, fragment, escapes, empty interior segments, or route
    patterns."""
    protocol_version: MCPInstanceProtocolProbeRequestProtocolVersion
    rpc_request: MCPProbeRPCRequest
    """Single MCP JSON-RPC initialize, initialized notification, discovery, or catalog-list request. Mutating and
    data-reading methods are rejected."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        host = self.host

        endpoint = self.endpoint

        protocol_version: str = self.protocol_version

        rpc_request = self.rpc_request.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "host": host,
                "endpoint": endpoint,
                "protocol_version": protocol_version,
                "rpc_request": rpc_request,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_probe_rpc_request import MCPProbeRPCRequest

        d = dict(src_dict)
        host = d.pop("host")

        endpoint = d.pop("endpoint")

        protocol_version = check_mcp_instance_protocol_probe_request_protocol_version(d.pop("protocol_version"))

        rpc_request = MCPProbeRPCRequest.from_dict(d.pop("rpc_request"))

        mcp_instance_protocol_probe_request = cls(
            host=host,
            endpoint=endpoint,
            protocol_version=protocol_version,
            rpc_request=rpc_request,
        )

        mcp_instance_protocol_probe_request.additional_properties = d
        return mcp_instance_protocol_probe_request

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
