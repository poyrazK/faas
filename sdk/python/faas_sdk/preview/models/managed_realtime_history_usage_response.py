from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ManagedRealtimeHistoryUsageResponse")


@_attrs_define
class ManagedRealtimeHistoryUsageResponse:
    """Account-scoped point-in-time retained history payload counts; not billing meters."""

    observed_at: datetime.datetime
    endpoint_count: int
    """Endpoints with a retained channel head."""
    channel_count: int
    """Retained channel heads, including empty heads."""
    stored_message_count: int
    """Message rows still physically present, including expired rows awaiting cleanup."""
    stored_payload_bytes: int
    """Decoded payload bytes in physically present rows; excludes database overhead."""
    replayable_message_count: int
    """Rows at or above each channel's current contiguous retention floor."""
    replayable_payload_bytes: int
    """Decoded payload bytes eligible for replay."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        observed_at = self.observed_at.isoformat()

        endpoint_count = self.endpoint_count

        channel_count = self.channel_count

        stored_message_count = self.stored_message_count

        stored_payload_bytes = self.stored_payload_bytes

        replayable_message_count = self.replayable_message_count

        replayable_payload_bytes = self.replayable_payload_bytes

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "observed_at": observed_at,
                "endpoint_count": endpoint_count,
                "channel_count": channel_count,
                "stored_message_count": stored_message_count,
                "stored_payload_bytes": stored_payload_bytes,
                "replayable_message_count": replayable_message_count,
                "replayable_payload_bytes": replayable_payload_bytes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        endpoint_count = d.pop("endpoint_count")

        channel_count = d.pop("channel_count")

        stored_message_count = d.pop("stored_message_count")

        stored_payload_bytes = d.pop("stored_payload_bytes")

        replayable_message_count = d.pop("replayable_message_count")

        replayable_payload_bytes = d.pop("replayable_payload_bytes")

        managed_realtime_history_usage_response = cls(
            observed_at=observed_at,
            endpoint_count=endpoint_count,
            channel_count=channel_count,
            stored_message_count=stored_message_count,
            stored_payload_bytes=stored_payload_bytes,
            replayable_message_count=replayable_message_count,
            replayable_payload_bytes=replayable_payload_bytes,
        )

        managed_realtime_history_usage_response.additional_properties = d
        return managed_realtime_history_usage_response

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
