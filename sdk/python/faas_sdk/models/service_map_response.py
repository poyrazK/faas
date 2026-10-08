from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.service_map_edge import ServiceMapEdge
    from ..models.service_map_node import ServiceMapNode


T = TypeVar("T", bound="ServiceMapResponse")


@_attrs_define
class ServiceMapResponse:
    """Account service map for `GET /v1/service-map` (ADR-732). `nodes` are
    the apps on at least one returned edge, sorted by slug; `edges` are
    sorted by `calls` descending. Degraded responses carry null `nodes`
    and `edges`.

    """

    range_: str
    source: str
    """"prometheus" or "degraded: <reason>"."""
    as_of: datetime.datetime
    truncated: bool
    nodes: list[ServiceMapNode] | None
    edges: list[ServiceMapEdge] | None
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        range_ = self.range_

        source = self.source

        as_of = self.as_of.isoformat()

        truncated = self.truncated

        nodes: list[dict[str, Any]] | None
        if isinstance(self.nodes, list):
            nodes = []
            for nodes_type_0_item_data in self.nodes:
                nodes_type_0_item = nodes_type_0_item_data.to_dict()
                nodes.append(nodes_type_0_item)

        else:
            nodes = self.nodes

        edges: list[dict[str, Any]] | None
        if isinstance(self.edges, list):
            edges = []
            for edges_type_0_item_data in self.edges:
                edges_type_0_item = edges_type_0_item_data.to_dict()
                edges.append(edges_type_0_item)

        else:
            edges = self.edges

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "range": range_,
                "source": source,
                "as_of": as_of,
                "truncated": truncated,
                "nodes": nodes,
                "edges": edges,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.service_map_edge import ServiceMapEdge
        from ..models.service_map_node import ServiceMapNode

        d = dict(src_dict)
        range_ = d.pop("range")

        source = d.pop("source")

        as_of = datetime.datetime.fromisoformat(d.pop("as_of"))

        truncated = d.pop("truncated")

        def _parse_nodes(data: object) -> list[ServiceMapNode] | None:
            if data is None:
                return data
            try:
                if not isinstance(data, list):
                    raise TypeError()
                nodes_type_0 = []
                _nodes_type_0 = data
                for nodes_type_0_item_data in _nodes_type_0:
                    nodes_type_0_item = ServiceMapNode.from_dict(nodes_type_0_item_data)

                    nodes_type_0.append(nodes_type_0_item)

                return nodes_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(list[ServiceMapNode] | None, data)

        nodes = _parse_nodes(d.pop("nodes"))

        def _parse_edges(data: object) -> list[ServiceMapEdge] | None:
            if data is None:
                return data
            try:
                if not isinstance(data, list):
                    raise TypeError()
                edges_type_0 = []
                _edges_type_0 = data
                for edges_type_0_item_data in _edges_type_0:
                    edges_type_0_item = ServiceMapEdge.from_dict(edges_type_0_item_data)

                    edges_type_0.append(edges_type_0_item)

                return edges_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(list[ServiceMapEdge] | None, data)

        edges = _parse_edges(d.pop("edges"))

        service_map_response = cls(
            range_=range_,
            source=source,
            as_of=as_of,
            truncated=truncated,
            nodes=nodes,
            edges=edges,
        )

        service_map_response.additional_properties = d
        return service_map_response

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
