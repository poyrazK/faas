from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.managed_realtime_channel_occupancy import ManagedRealtimeChannelOccupancy


T = TypeVar("T", bound="ManagedRealtimeOccupancyResponse")


@_attrs_define
class ManagedRealtimeOccupancyResponse:
    """Point-in-time channel occupancy across the realtime fleet."""

    channels: list[ManagedRealtimeChannelOccupancy]
    partial: bool
    """True when some active realtime nodes did not answer and counts may be low."""
    nodes_queried: int
    nodes_unavailable: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        channels = []
        for channels_item_data in self.channels:
            channels_item = channels_item_data.to_dict()
            channels.append(channels_item)

        partial = self.partial

        nodes_queried = self.nodes_queried

        nodes_unavailable = self.nodes_unavailable

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "channels": channels,
                "partial": partial,
                "nodes_queried": nodes_queried,
                "nodes_unavailable": nodes_unavailable,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_realtime_channel_occupancy import ManagedRealtimeChannelOccupancy

        d = dict(src_dict)
        channels = []
        _channels = d.pop("channels")
        for channels_item_data in _channels:
            channels_item = ManagedRealtimeChannelOccupancy.from_dict(channels_item_data)

            channels.append(channels_item)

        partial = d.pop("partial")

        nodes_queried = d.pop("nodes_queried")

        nodes_unavailable = d.pop("nodes_unavailable")

        managed_realtime_occupancy_response = cls(
            channels=channels,
            partial=partial,
            nodes_queried=nodes_queried,
            nodes_unavailable=nodes_unavailable,
        )

        managed_realtime_occupancy_response.additional_properties = d
        return managed_realtime_occupancy_response

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
