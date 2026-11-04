from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RealtimePublisherCredentialRequest")


@_attrs_define
class RealtimePublisherCredentialRequest:
    """Realtime Publisher Credential Request."""

    name: str
    channels: list[str]
    event_types: list[str]
    max_message_ttl_seconds: int | Unset = 0
    """Message TTL cap; zero is uncapped. Positive caps require a positive message TTL at or below the cap."""
    publishes_per_minute: int | Unset = 60
    max_payload_bytes: int | Unset = 4096
    ttl_seconds: int | Unset = 86400
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        channels = self.channels

        event_types = self.event_types

        max_message_ttl_seconds = self.max_message_ttl_seconds

        publishes_per_minute = self.publishes_per_minute

        max_payload_bytes = self.max_payload_bytes

        ttl_seconds = self.ttl_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "channels": channels,
                "event_types": event_types,
            }
        )
        if max_message_ttl_seconds is not UNSET:
            field_dict["max_message_ttl_seconds"] = max_message_ttl_seconds
        if publishes_per_minute is not UNSET:
            field_dict["publishes_per_minute"] = publishes_per_minute
        if max_payload_bytes is not UNSET:
            field_dict["max_payload_bytes"] = max_payload_bytes
        if ttl_seconds is not UNSET:
            field_dict["ttl_seconds"] = ttl_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        channels = cast(list[str], d.pop("channels"))

        event_types = cast(list[str], d.pop("event_types"))

        max_message_ttl_seconds = d.pop("max_message_ttl_seconds", UNSET)

        publishes_per_minute = d.pop("publishes_per_minute", UNSET)

        max_payload_bytes = d.pop("max_payload_bytes", UNSET)

        ttl_seconds = d.pop("ttl_seconds", UNSET)

        realtime_publisher_credential_request = cls(
            name=name,
            channels=channels,
            event_types=event_types,
            max_message_ttl_seconds=max_message_ttl_seconds,
            publishes_per_minute=publishes_per_minute,
            max_payload_bytes=max_payload_bytes,
            ttl_seconds=ttl_seconds,
        )

        realtime_publisher_credential_request.additional_properties = d
        return realtime_publisher_credential_request

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
