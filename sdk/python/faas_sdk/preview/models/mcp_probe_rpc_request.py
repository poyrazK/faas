from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, Literal, TypeVar, cast

from attrs import define as _attrs_define

from ..models.mcp_probe_rpc_request_method import MCPProbeRPCRequestMethod, check_mcp_probe_rpc_request_method
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_probe_rpc_request_params import MCPProbeRPCRequestParams


T = TypeVar("T", bound="MCPProbeRPCRequest")


@_attrs_define
class MCPProbeRPCRequest:
    """Single MCP JSON-RPC initialize, initialized notification, discovery, or catalog-list request. Mutating and data-
    reading methods are rejected.

    """

    jsonrpc: Literal["2.0"]
    method: MCPProbeRPCRequestMethod
    id: float | str | Unset = UNSET
    params: MCPProbeRPCRequestParams | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        jsonrpc = self.jsonrpc

        method: str = self.method

        id: float | str | Unset
        if isinstance(self.id, Unset):
            id = UNSET
        else:
            id = self.id

        params: dict[str, Any] | Unset = UNSET
        if not isinstance(self.params, Unset):
            params = self.params.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "jsonrpc": jsonrpc,
                "method": method,
            }
        )
        if id is not UNSET:
            field_dict["id"] = id
        if params is not UNSET:
            field_dict["params"] = params

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_probe_rpc_request_params import MCPProbeRPCRequestParams

        d = dict(src_dict)
        jsonrpc = cast(Literal["2.0"], d.pop("jsonrpc"))
        if jsonrpc != "2.0":
            raise ValueError(f"jsonrpc must match const '2.0', got '{jsonrpc}'")

        method = check_mcp_probe_rpc_request_method(d.pop("method"))

        def _parse_id(data: object) -> float | str | Unset:
            if isinstance(data, Unset):
                return data
            return cast(float | str | Unset, data)

        id = _parse_id(d.pop("id", UNSET))

        _params = d.pop("params", UNSET)
        params: MCPProbeRPCRequestParams | Unset
        if isinstance(_params, Unset):
            params = UNSET
        else:
            params = MCPProbeRPCRequestParams.from_dict(_params)

        mcp_probe_rpc_request = cls(
            jsonrpc=jsonrpc,
            method=method,
            id=id,
            params=params,
        )

        return mcp_probe_rpc_request
