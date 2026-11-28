from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.git_hub_account_connection import GitHubAccountConnection


T = TypeVar("T", bound="GitHubAccountConnections")


@_attrs_define
class GitHubAccountConnections:
    """This account's deployment connections and the CSRF token for subsequent management actions."""

    csrf_token: str
    connections: list[GitHubAccountConnection]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        csrf_token = self.csrf_token

        connections = []
        for connections_item_data in self.connections:
            connections_item = connections_item_data.to_dict()
            connections.append(connections_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "csrf_token": csrf_token,
                "connections": connections,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.git_hub_account_connection import GitHubAccountConnection

        d = dict(src_dict)
        csrf_token = d.pop("csrf_token")

        connections = []
        _connections = d.pop("connections")
        for connections_item_data in _connections:
            connections_item = GitHubAccountConnection.from_dict(connections_item_data)

            connections.append(connections_item)

        git_hub_account_connections = cls(
            csrf_token=csrf_token,
            connections=connections,
        )

        git_hub_account_connections.additional_properties = d
        return git_hub_account_connections

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
