from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedRealtimePublishResponse")


@_attrs_define
class ManagedRealtimePublishResponse:
    """Result of publishing a message to an endpoint-scoped channel."""

    queued: int
    """Number of local owner queues that accepted the message."""
    partial: bool | Unset = UNSET
    """Whether one or more active realtime nodes did not accept the publish."""
    nodes_queried: int | Unset = UNSET
    """Active realtime nodes that accepted the publish request."""
    nodes_unavailable: int | Unset = UNSET
    """Active realtime nodes that did not accept the publish request."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        queued = self.queued

        partial = self.partial

        nodes_queried = self.nodes_queried

        nodes_unavailable = self.nodes_unavailable

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "queued": queued,
            }
        )
        if partial is not UNSET:
            field_dict["partial"] = partial
        if nodes_queried is not UNSET:
            field_dict["nodes_queried"] = nodes_queried
        if nodes_unavailable is not UNSET:
            field_dict["nodes_unavailable"] = nodes_unavailable

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        queued = d.pop("queued")

        partial = d.pop("partial", UNSET)

        nodes_queried = d.pop("nodes_queried", UNSET)

        nodes_unavailable = d.pop("nodes_unavailable", UNSET)

        managed_realtime_publish_response = cls(
            queued=queued,
            partial=partial,
            nodes_queried=nodes_queried,
            nodes_unavailable=nodes_unavailable,
        )

        managed_realtime_publish_response.additional_properties = d
        return managed_realtime_publish_response

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
