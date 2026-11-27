from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.mcp_listing_install_client_config import MCPListingInstallClientConfig


T = TypeVar("T", bound="MCPListingInstall")


@_attrs_define
class MCPListingInstall:
    """Ways to add a listed MCP server to a client."""

    cursor_url: str
    vscode_url: str
    client_config: MCPListingInstallClientConfig
    """An mcpServers client configuration."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        cursor_url = self.cursor_url

        vscode_url = self.vscode_url

        client_config = self.client_config.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "cursor_url": cursor_url,
                "vscode_url": vscode_url,
                "client_config": client_config,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_listing_install_client_config import MCPListingInstallClientConfig

        d = dict(src_dict)
        cursor_url = d.pop("cursor_url")

        vscode_url = d.pop("vscode_url")

        client_config = MCPListingInstallClientConfig.from_dict(d.pop("client_config"))

        mcp_listing_install = cls(
            cursor_url=cursor_url,
            vscode_url=vscode_url,
            client_config=client_config,
        )

        mcp_listing_install.additional_properties = d
        return mcp_listing_install

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
