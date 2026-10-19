from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.mcp_readiness_o_auth_connection import MCPReadinessOAuthConnection


T = TypeVar("T", bound="MCPReadinessMonitorConfigOauthConnections")


@_attrs_define
class MCPReadinessMonitorConfigOauthConnections:
    """Environment scope to pinned connection. Mutually exclusive with oauth_connection and secret_ref. Missing
    environments fail closed.

    """

    additional_properties: dict[str, MCPReadinessOAuthConnection] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}
        for prop_name, prop in self.additional_properties.items():
            field_dict[prop_name] = prop.to_dict()

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_readiness_o_auth_connection import MCPReadinessOAuthConnection

        d = dict(src_dict)
        mcp_readiness_monitor_config_oauth_connections = cls()

        additional_properties = {}
        for prop_name, prop_dict in d.items():
            additional_property = MCPReadinessOAuthConnection.from_dict(prop_dict)

            additional_properties[prop_name] = additional_property

        mcp_readiness_monitor_config_oauth_connections.additional_properties = additional_properties
        return mcp_readiness_monitor_config_oauth_connections

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> MCPReadinessOAuthConnection:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: MCPReadinessOAuthConnection) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
