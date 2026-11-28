from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_listing_tool import MCPListingTool
    from ..models.put_mcp_listing_request_contract import PutMCPListingRequestContract


T = TypeVar("T", bound="PutMCPListingRequest")


@_attrs_define
class PutMCPListingRequest:
    """A sold MCP server's listing to publish: the tools are those the server lists now."""

    title: str
    endpoint: str
    """The MCP endpoint, https on the app's own host."""
    tools: list[MCPListingTool]
    summary: str | Unset = UNSET
    """One or two sentences on what the server does."""
    registry_name: str | Unset = UNSET
    """MCP registry name, such as io.github.<user>/<server>; enables server.json."""
    show_usage: bool | Unset = UNSET
    """Show the open customer count on the page."""
    contract: PutMCPListingRequestContract | Unset = UNSET
    """The live server's format-2 MCP catalog contract, at most 1,000,000 bytes. Production deploys are then held
    to it. Without it a republish keeps the contract production deploys are held to now, and its probe_secret,
    unless drop_contract is set."""
    drop_contract: bool | Unset = UNSET
    """Republish without a contract, which ends the tool contract gate. Exclusive with contract."""
    probe_secret: str | Unset = UNSET
    """App secret holding a bearer token the gate sends when listing a candidate's tools, for a server that checks
    credentials itself. Needs a contract, sent or kept; empty on a republish that keeps the contract keeps the
    current one."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        title = self.title

        endpoint = self.endpoint

        tools = []
        for tools_item_data in self.tools:
            tools_item = tools_item_data.to_dict()
            tools.append(tools_item)

        summary = self.summary

        registry_name = self.registry_name

        show_usage = self.show_usage

        contract: dict[str, Any] | Unset = UNSET
        if not isinstance(self.contract, Unset):
            contract = self.contract.to_dict()

        drop_contract = self.drop_contract

        probe_secret = self.probe_secret

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "title": title,
                "endpoint": endpoint,
                "tools": tools,
            }
        )
        if summary is not UNSET:
            field_dict["summary"] = summary
        if registry_name is not UNSET:
            field_dict["registry_name"] = registry_name
        if show_usage is not UNSET:
            field_dict["show_usage"] = show_usage
        if contract is not UNSET:
            field_dict["contract"] = contract
        if drop_contract is not UNSET:
            field_dict["drop_contract"] = drop_contract
        if probe_secret is not UNSET:
            field_dict["probe_secret"] = probe_secret

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_listing_tool import MCPListingTool
        from ..models.put_mcp_listing_request_contract import PutMCPListingRequestContract

        d = dict(src_dict)
        title = d.pop("title")

        endpoint = d.pop("endpoint")

        tools = []
        _tools = d.pop("tools")
        for tools_item_data in _tools:
            tools_item = MCPListingTool.from_dict(tools_item_data)

            tools.append(tools_item)

        summary = d.pop("summary", UNSET)

        registry_name = d.pop("registry_name", UNSET)

        show_usage = d.pop("show_usage", UNSET)

        _contract = d.pop("contract", UNSET)
        contract: PutMCPListingRequestContract | Unset
        if isinstance(_contract, Unset):
            contract = UNSET
        else:
            contract = PutMCPListingRequestContract.from_dict(_contract)

        drop_contract = d.pop("drop_contract", UNSET)

        probe_secret = d.pop("probe_secret", UNSET)

        put_mcp_listing_request = cls(
            title=title,
            endpoint=endpoint,
            tools=tools,
            summary=summary,
            registry_name=registry_name,
            show_usage=show_usage,
            contract=contract,
            drop_contract=drop_contract,
            probe_secret=probe_secret,
        )

        put_mcp_listing_request.additional_properties = d
        return put_mcp_listing_request

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
