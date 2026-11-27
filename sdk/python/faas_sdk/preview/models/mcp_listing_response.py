from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_listing_tool import MCPListingTool


T = TypeVar("T", bound="MCPListingResponse")


@_attrs_define
class MCPListingResponse:
    """A sold MCP server's published listing, as its owner sees it."""

    app: str
    title: str
    summary: str
    """What the server does, as the seller wrote it."""
    endpoint: str
    tools: list[MCPListingTool]
    show_usage: bool
    version: int
    """Bumped on every publish."""
    published_at: datetime.datetime
    page_url: str
    """The public listing page."""
    registry_name: str | Unset = UNSET
    server_json_url: str | Unset = UNSET
    """The registry server.json, when registry_name is set."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app = self.app

        title = self.title

        summary = self.summary

        endpoint = self.endpoint

        tools = []
        for tools_item_data in self.tools:
            tools_item = tools_item_data.to_dict()
            tools.append(tools_item)

        show_usage = self.show_usage

        version = self.version

        published_at = self.published_at.isoformat()

        page_url = self.page_url

        registry_name = self.registry_name

        server_json_url = self.server_json_url

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app": app,
                "title": title,
                "summary": summary,
                "endpoint": endpoint,
                "tools": tools,
                "show_usage": show_usage,
                "version": version,
                "published_at": published_at,
                "page_url": page_url,
            }
        )
        if registry_name is not UNSET:
            field_dict["registry_name"] = registry_name
        if server_json_url is not UNSET:
            field_dict["server_json_url"] = server_json_url

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_listing_tool import MCPListingTool

        d = dict(src_dict)
        app = d.pop("app")

        title = d.pop("title")

        summary = d.pop("summary")

        endpoint = d.pop("endpoint")

        tools = []
        _tools = d.pop("tools")
        for tools_item_data in _tools:
            tools_item = MCPListingTool.from_dict(tools_item_data)

            tools.append(tools_item)

        show_usage = d.pop("show_usage")

        version = d.pop("version")

        published_at = datetime.datetime.fromisoformat(d.pop("published_at"))

        page_url = d.pop("page_url")

        registry_name = d.pop("registry_name", UNSET)

        server_json_url = d.pop("server_json_url", UNSET)

        mcp_listing_response = cls(
            app=app,
            title=title,
            summary=summary,
            endpoint=endpoint,
            tools=tools,
            show_usage=show_usage,
            version=version,
            published_at=published_at,
            page_url=page_url,
            registry_name=registry_name,
            server_json_url=server_json_url,
        )

        mcp_listing_response.additional_properties = d
        return mcp_listing_response

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
