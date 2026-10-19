from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ManagedRealtimeChannelOccupancy")


@_attrs_define
class ManagedRealtimeChannelOccupancy:
    """Fleet-wide subscription and presence counts for one channel."""

    channel: str
    subscribers: int
    """Open subscriptions on every transport (WebSocket"""
    presence_members: int
    """Distinct presence members; a principal-scoped member counts once."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        channel = self.channel

        subscribers = self.subscribers

        presence_members = self.presence_members

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "channel": channel,
                "subscribers": subscribers,
                "presence_members": presence_members,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        channel = d.pop("channel")

        subscribers = d.pop("subscribers")

        presence_members = d.pop("presence_members")

        managed_realtime_channel_occupancy = cls(
            channel=channel,
            subscribers=subscribers,
            presence_members=presence_members,
        )

        managed_realtime_channel_occupancy.additional_properties = d
        return managed_realtime_channel_occupancy

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
