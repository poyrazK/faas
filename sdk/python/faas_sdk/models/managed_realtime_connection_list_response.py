from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.managed_realtime_connection_response import ManagedRealtimeConnectionResponse


T = TypeVar("T", bound="ManagedRealtimeConnectionListResponse")


@_attrs_define
class ManagedRealtimeConnectionListResponse:
    """Bounded point-in-time live connection inventory."""

    connections: list[ManagedRealtimeConnectionResponse]
    limit: int
    truncated: bool
    partial: bool
    """True when one or more active realtime nodes did not answer."""
    nodes_queried: int
    nodes_unavailable: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        connections = []
        for connections_item_data in self.connections:
            connections_item = connections_item_data.to_dict()
            connections.append(connections_item)

        limit = self.limit

        truncated = self.truncated

        partial = self.partial

        nodes_queried = self.nodes_queried

        nodes_unavailable = self.nodes_unavailable

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "connections": connections,
                "limit": limit,
                "truncated": truncated,
                "partial": partial,
                "nodes_queried": nodes_queried,
                "nodes_unavailable": nodes_unavailable,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_realtime_connection_response import ManagedRealtimeConnectionResponse

        d = dict(src_dict)
        connections = []
        _connections = d.pop("connections")
        for connections_item_data in _connections:
            connections_item = ManagedRealtimeConnectionResponse.from_dict(connections_item_data)

            connections.append(connections_item)

        limit = d.pop("limit")

        truncated = d.pop("truncated")

        partial = d.pop("partial")

        nodes_queried = d.pop("nodes_queried")

        nodes_unavailable = d.pop("nodes_unavailable")

        managed_realtime_connection_list_response = cls(
            connections=connections,
            limit=limit,
            truncated=truncated,
            partial=partial,
            nodes_queried=nodes_queried,
            nodes_unavailable=nodes_unavailable,
        )

        managed_realtime_connection_list_response.additional_properties = d
        return managed_realtime_connection_list_response

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
