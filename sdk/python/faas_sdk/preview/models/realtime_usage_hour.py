from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RealtimeUsageHour")


@_attrs_define
class RealtimeUsageHour:
    """Realtime usage in one UTC hour."""

    hour: datetime.datetime
    """Start of the UTC hour."""
    peak_connections: int
    """Most connections open at once in any minute of the hour."""
    connection_seconds: int
    """Connections held multiplied by seconds."""
    delivered_messages: int
    """Messages sent to clients"""
    delivered_bytes: int
    """Bytes of delivered messages."""
    received_messages: int
    """Client messages forwarded to the app."""
    received_bytes: int
    """Bytes of received client messages."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        hour = self.hour.isoformat()

        peak_connections = self.peak_connections

        connection_seconds = self.connection_seconds

        delivered_messages = self.delivered_messages

        delivered_bytes = self.delivered_bytes

        received_messages = self.received_messages

        received_bytes = self.received_bytes

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "hour": hour,
                "peak_connections": peak_connections,
                "connection_seconds": connection_seconds,
                "delivered_messages": delivered_messages,
                "delivered_bytes": delivered_bytes,
                "received_messages": received_messages,
                "received_bytes": received_bytes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        hour = datetime.datetime.fromisoformat(d.pop("hour"))

        peak_connections = d.pop("peak_connections")

        connection_seconds = d.pop("connection_seconds")

        delivered_messages = d.pop("delivered_messages")

        delivered_bytes = d.pop("delivered_bytes")

        received_messages = d.pop("received_messages")

        received_bytes = d.pop("received_bytes")

        realtime_usage_hour = cls(
            hour=hour,
            peak_connections=peak_connections,
            connection_seconds=connection_seconds,
            delivered_messages=delivered_messages,
            delivered_bytes=delivered_bytes,
            received_messages=received_messages,
            received_bytes=received_bytes,
        )

        realtime_usage_hour.additional_properties = d
        return realtime_usage_hour

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
